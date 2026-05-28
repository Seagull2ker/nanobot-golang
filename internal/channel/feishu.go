package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
	"github.com/Seagull2ker/nanobot-go/internal/types"
)

// ---------- Config ----------

// FeishuConfig holds Feishu (Lark) channel credentials and settings.
type FeishuConfig struct {
	AppID             string   `json:"appId" yaml:"app_id"`
	AppSecret         string   `json:"appSecret" yaml:"app_secret"`
	VerificationToken string   `json:"verificationToken" yaml:"verification_token"`
	EncryptKey        string   `json:"encryptKey" yaml:"encrypt_key,omitempty"`
	AllowFrom         []string `json:"allowFrom,omitempty" yaml:"allow_from,omitempty"`
	GroupPolicy       string   `json:"groupPolicy,omitempty" yaml:"group_policy,omitempty"`
}

// ---------- Constants ----------

const feishuCardMaxRunes = 3000

// ---------- FeishuChannel ----------

// FeishuChannel handles Feishu (Lark) messaging via WebSocket long connection.
//
// Uses the Lark SDK's WebSocket client for receiving messages — more reliable
// than HTTP webhooks (no public URL needed, auto-reconnect).
//
// Token management: the Lark SDK client handles tenant_access_token caching
// and auto-refresh internally.
type FeishuChannel struct {
	client *lark.Client
	bus    *bus.MessageBus
	config FeishuConfig

	processedMsgs sync.Map // message_id → true, for dedup

	lifecycleMu sync.Mutex
	stopWS      context.CancelFunc
	wsDone      chan struct{}
}

// NewFeishuChannel creates a Feishu channel.
func NewFeishuChannel(cfg FeishuConfig, messageBus *bus.MessageBus) *FeishuChannel {
	return &FeishuChannel{
		client: lark.NewClient(cfg.AppID, cfg.AppSecret),
		bus:    messageBus,
		config: cfg,
	}
}

func (c *FeishuChannel) Name() string            { return "feishu" }
func (c *FeishuChannel) SupportsStreaming() bool { return false }

// ---------- Lifecycle ----------

// Start connects to Feishu via WebSocket and begins processing messages.
// Safe to call once; subsequent calls are no-ops.
func (c *FeishuChannel) Start(ctx context.Context, bus *bus.MessageBus) error {
	c.bus = bus

	c.lifecycleMu.Lock()
	if c.stopWS != nil {
		c.lifecycleMu.Unlock()
		return nil // already started
	}

	wsCtx, wsCancel := context.WithCancel(ctx)
	c.stopWS = wsCancel
	c.wsDone = make(chan struct{})
	c.lifecycleMu.Unlock()

	// Register event handler.
	d := dispatcher.NewEventDispatcher(c.config.VerificationToken, c.config.EncryptKey)
	d.OnP2MessageReceiveV1(c.onMessage)

	// Start WebSocket client.
	wsClient := larkws.NewClient(c.config.AppID, c.config.AppSecret,
		larkws.WithEventHandler(d),
		larkws.WithLogLevel(larkcore.LogLevelError),
	)

	go func() {
		defer close(c.wsDone)
		slog.Info("feishu websocket connecting")
		if err := wsClient.Start(wsCtx); err != nil {
			slog.Error("feishu websocket stopped", "error", err)
		}
	}()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("feishu consumeOutbound panic", "panic", r)
			}
		}()
		c.consumeOutbound(ctx)
	}()
	return nil
}

// Stop disconnects the WebSocket and waits for cleanup.
func (c *FeishuChannel) Stop(ctx context.Context) error {
	c.lifecycleMu.Lock()
	stop := c.stopWS
	done := c.wsDone
	c.stopWS = nil
	c.wsDone = nil
	c.lifecycleMu.Unlock()

	if stop == nil {
		return nil
	}

	stop()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---------- Inbound: message receive → MessageBus ----------

func (c *FeishuChannel) onMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	data := event.Event
	if data == nil || data.Message == nil || data.Sender == nil {
		return nil
	}

	msg := data.Message
	sender := data.Sender

	msgID := safeString(msg.MessageId)
	if msgID == "" {
		return nil
	}

	// Dedup.
	if _, loaded := c.processedMsgs.LoadOrStore(msgID, true); loaded {
		return nil
	}

	// Sender access control.
	senderID := ""
	if sender.SenderId != nil && sender.SenderId.OpenId != nil {
		senderID = *sender.SenderId.OpenId
	}
	if !isSenderAllowed("feishu", senderID, c.config.AllowFrom) {
		return nil
	}

	// Extract content.
	content := c.extractContent(msg.MessageType, msg.Content)
	if content == "" {
		return nil
	}

	// Group message policy.
	chatType := safeString(msg.ChatType)
	if chatType == "group" && !c.shouldProcessGroup(content) {
		return nil
	}

	// Strip @mentions.
	content = normalizeFeishuText(content)

	c.bus.PublishInbound(ctx, &types.InboundMessage{
		Channel:   "feishu",
		SenderID:  senderID,
		ChatID:    safeString(msg.ChatId),
		Content:   content,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"message_id": msgID,
			"root_id":    safeString(msg.RootId),
			"parent_id":  safeString(msg.ParentId),
		},
	})
	return nil
}

// ---------- Content extraction ----------

func (c *FeishuChannel) extractContent(msgType *string, content *string) string {
	if content == nil {
		return ""
	}
	raw := *content

	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return raw
	}

	mt := "text"
	if msgType != nil {
		mt = *msgType
	}

	switch mt {
	case "text":
		if t, ok := parsed["text"].(string); ok {
			return t
		}
		return raw
	case "post":
		if text := extractPostText(parsed); text != "" {
			return text
		}
		return "[post]"
	case "share_chat":
		return "[shared chat]"
	case "share_user":
		return "[shared user]"
	case "interactive":
		return extractInteractiveContent(parsed)
	case "share_calendar_event":
		return "[shared calendar event]"
	case "system":
		return "[system message]"
	case "merge_forward":
		return "[merged forward messages]"
	default:
		return fmt.Sprintf("[%s]", mt)
	}
}

func extractPostText(parsed map[string]any) string {
	// Direct format: {"content": [[{"tag":"text","text":"..."}]]}
	if contentBlock, ok := parsed["content"]; ok {
		return extractPostSegments(contentBlock)
	}

	// Localized format: {"zh_cn": {"content": ...}, "en_us": {"content": ...}}
	for _, lang := range []string{"zh_cn", "en_us", "ja_jp"} {
		if locale, ok := parsed[lang].(map[string]any); ok {
			if text := extractPostSegments(locale["content"]); text != "" {
				return text
			}
		}
	}

	// Wrapped format: {"post": {"zh_cn": {"content": ...}}}
	if post, ok := parsed["post"].(map[string]any); ok {
		for _, lang := range []string{"zh_cn", "en_us", "ja_jp"} {
			if locale, ok := post[lang].(map[string]any); ok {
				if text := extractPostSegments(locale["content"]); text != "" {
					return text
				}
			}
		}
	}

	return ""
}

func extractPostSegments(content any) string {
	lines, ok := content.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, line := range lines {
		segments, ok := line.([]any)
		if !ok {
			continue
		}
		for _, seg := range segments {
			s, ok := seg.(map[string]any)
			if !ok {
				continue
			}
			tag, _ := s["tag"].(string)
			switch tag {
			case "text":
				if t, ok := s["text"].(string); ok {
					parts = append(parts, t)
				}
			case "a":
				if t, ok := s["text"].(string); ok {
					parts = append(parts, t)
				}
			case "at":
				if name, ok := s["user_name"].(string); ok {
					parts = append(parts, "@"+name)
				}
			}
		}
	}
	return strings.Join(parts, "")
}

func extractInteractiveContent(parsed map[string]any) string {
	var parts []string

	if title, ok := parsed["title"].(string); ok && title != "" {
		parts = append(parts, title)
	}
	if header, ok := parsed["header"].(map[string]any); ok {
		if t, ok := header["title"].(string); ok {
			parts = append(parts, t)
		}
	}
	if elements, ok := parsed["elements"].([]any); ok {
		parts = append(parts, extractElements(elements)...)
	}
	if card, ok := parsed["card"].(map[string]any); ok {
		parts = append(parts, extractInteractiveContent(card))
	}

	return strings.Join(parts, "\n")
}

func extractElements(elements []any) []string {
	var parts []string
	for _, el := range elements {
		m, ok := el.(map[string]any)
		if !ok {
			continue
		}
		tag, _ := m["tag"].(string)
		switch tag {
		case "markdown", "lark_md":
			if t, ok := m["content"].(string); ok {
				parts = append(parts, t)
			}
		case "div":
			if t, ok := m["text"].(map[string]any); ok {
				if text, ok := t["content"].(string); ok {
					parts = append(parts, text)
				}
			}
			if fields, ok := m["fields"].([]any); ok {
				for _, f := range fields {
					if fm, ok := f.(map[string]any); ok {
						if text, ok := fm["content"].(string); ok {
							parts = append(parts, text)
						}
					}
				}
			}
		case "button":
			if t, ok := m["text"].(map[string]any); ok {
				if text, ok := t["content"].(string); ok {
					parts = append(parts, text)
				}
			}
		case "note":
			if els, ok := m["elements"].([]any); ok {
				parts = append(parts, extractElements(els)...)
			}
		case "column_set":
			if cols, ok := m["columns"].([]any); ok {
				for _, col := range cols {
					if cm, ok := col.(map[string]any); ok {
						if els, ok := cm["elements"].([]any); ok {
							parts = append(parts, extractElements(els)...)
						}
					}
				}
			}
		case "plain_text":
			if t, ok := m["content"].(string); ok {
				parts = append(parts, t)
			}
		default:
			if els, ok := m["elements"].([]any); ok {
				parts = append(parts, extractElements(els)...)
			}
		}
	}
	return parts
}

// ---------- Group message policy ----------

func (c *FeishuChannel) shouldProcessGroup(content string) bool {
	switch c.config.GroupPolicy {
	case "open":
		return true
	default: // "mention" or empty
		return strings.Contains(content, "@_user_") || strings.Contains(content, "<at ")
	}
}

func normalizeFeishuText(text string) string {
	// Strip "@_user_ " prefix (Feishu @mention marker).
	if idx := strings.Index(text, "@_user_"); idx >= 0 {
		rest := text[idx+8:]
		if rest != "" {
			return strings.TrimSpace(rest)
		}
	}
	return text
}

// ---------- Sender access control ----------

// isSenderAllowed checks if senderID is in the allow list.
// nil/empty list → deny all. ["*"] → allow all.
func isSenderAllowed(channelName, senderID string, allowFrom []string) bool {
	if len(allowFrom) == 0 {
		slog.Debug("feishu outbound skipped — no allowFrom", "channel", channelName)
		return false
	}
	for _, allowed := range allowFrom {
		if allowed == "*" || allowed == senderID {
			return true
		}
	}
	return false
}

// ---------- Outbound: MessageBus → Feishu Send ----------

func (c *FeishuChannel) consumeOutbound(ctx context.Context) {
	ch := c.bus.ConsumeOutbound()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if msg.Channel != "feishu" {
				slog.Debug("feishu outbound skipped — not feishu channel", "channel", msg.Channel, "chat_id", msg.ChatID)
				continue
			}
			// Skip progress messages.
			if isProgress, _ := msg.Metadata["_progress"].(bool); isProgress {
				slog.Debug("feishu outbound skipped — progress message", "chat_id", msg.ChatID)
				continue
			}
			slog.Info("feishu outbound message received", "chat_id", msg.ChatID, "content_len", len(msg.Content))
			if err := c.Send(ctx, msg); err != nil {
				slog.Error("feishu outbound failed", "error", err, "chat_id", msg.ChatID)
			}
		}
	}
}

// Send delivers a message to a Feishu chat.
func (c *FeishuChannel) Send(ctx context.Context, msg *types.OutboundMessage) error {
	chunks, ok := c.buildCardChunks(msg.Content, msg.Metadata)
	if !ok || len(chunks) == 0 {
		return nil
	}

	// replyTo := c.pickReplyTarget(msg)

	// // Try threaded reply for first chunk.
	// if replyTo != "" {
	// 	if err := c.replyWithFallback(ctx, msg.ChatID, replyTo, chunks); err == nil {
	// 		return nil
	// 	}
	// }

	// Send as new messages.
	for _, chunk := range chunks {
		if err := c.sendCard(ctx, msg.ChatID, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (c *FeishuChannel) pickReplyTarget(msg *types.OutboundMessage) string {
	if msg.ReplyTo != "" {
		return msg.ReplyTo
	}
	if id, ok := msg.Metadata["message_id"].(string); ok {
		return id
	}
	return ""
}

// replyWithFallback tries to send as a threaded reply, falling back to new messages.
func (c *FeishuChannel) replyWithFallback(ctx context.Context, chatID, replyTo string, chunks []string) error {
	first := chunks[0]
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(replyTo).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			Content(first).
			MsgType("interactive").
			ReplyInThread(true).
			Build()).
		Build()

	resp, err := c.client.Im.Message.Reply(ctx, req)
	if err != nil || !resp.Success() {
		// Fallback: send as create.
		return c.sendCard(ctx, chatID, first)
	}

	for _, chunk := range chunks[1:] {
		if err := c.sendCard(ctx, chatID, chunk); err != nil {
			return err
		}
	}
	return nil
}

// sendCard sends a single interactive card message.
func (c *FeishuChannel) sendCard(ctx context.Context, chatID, cardJSON string) error {
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("interactive").
			Content(cardJSON).
			Build()).
		Build()

	resp, err := c.client.Im.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("feishu send: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishu send: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// ---------- Message card building ----------

// buildCardChunks converts content into Feishu interactive card JSON chunks.
func (c *FeishuChannel) buildCardChunks(content string, metadata map[string]any) ([]string, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, false
	}

	// Format tool hints specially.
	if isToolHint, _ := metadata["_tool_hint"].(bool); isToolHint {
		content = "**Tool Calls**\n\n```text\n" + content + "\n```"
	}

	// Convert Markdown to Feishu card format.
	content = convertMarkdownToFeishu(content)

	// Split into <=3000 rune chunks.
	var chunks []string
	for _, chunk := range splitByRunes(content, feishuCardMaxRunes) {
		card := fmt.Sprintf(`{"config":{"wide_screen_mode":true},"elements":[{"tag":"markdown","content":%s}]}`,
			jsonString(chunk))
		chunks = append(chunks, card)
	}

	return chunks, true
}

func splitByRunes(s string, maxRunes int) []string {
	var chunks []string
	runes := []rune(s)
	for i := 0; i < len(runes); i += maxRunes {
		end := i + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

// ---------- Markdown → Feishu card conversion ----------

// convertMarkdownToFeishu adapts standard Markdown to Feishu card Markdown subset.
// Feishu cards don't support headings (#), tables (|), or blockquotes (>),
// so these are converted to alternatives.
func convertMarkdownToFeishu(content string) string {
	var lines []string
	inTable := false
	var tableRows [][]string

	flushTable := func() {
		if !inTable || len(tableRows) == 0 {
			return
		}
		headers := tableRows[0]
		for _, row := range tableRows[1:] {
			var parts []string
			for j, cell := range row {
				header := ""
				if j < len(headers) {
					header = headers[j]
				}
				parts = append(parts, fmt.Sprintf("**%s:** %s", header, cell))
			}
			lines = append(lines, strings.Join(parts, " | "))
		}
		tableRows = nil
		inTable = false
	}

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		// Table detection and collection.
		if strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed, "|") {
			if strings.Contains(trimmed, "---") {
				continue // separator row
			}
			cells := strings.Split(trimmed, "|")
			var row []string
			for _, c := range cells {
				c = strings.TrimSpace(c)
				if c != "" {
					row = append(row, c)
				}
			}
			tableRows = append(tableRows, row)
			inTable = true
			continue
		} else {
			flushTable()
		}

		// Heading conversion: ### text → **text**
		if strings.HasPrefix(trimmed, "### ") {
			lines = append(lines, "**"+strings.TrimPrefix(trimmed, "### ")+"**")
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			lines = append(lines, "**"+strings.TrimPrefix(trimmed, "## ")+"**")
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			lines = append(lines, "**"+strings.TrimPrefix(trimmed, "# ")+"**")
			continue
		}

		// Blockquote: > text → *text*
		if strings.HasPrefix(trimmed, "> ") {
			text := strings.TrimPrefix(trimmed, "> ")
			// Remove nested "> "
			for strings.HasPrefix(text, "> ") {
				text = strings.TrimPrefix(text, "> ")
			}
			lines = append(lines, "*"+text+"*")
			continue
		}

		lines = append(lines, line)
	}

	flushTable()
	return strings.Join(lines, "\n")
}

// ---------- Helpers ----------

func (c *FeishuChannel) containsInternalURL(command string) bool {
	return strings.Contains(command, "169.254.169.254") ||
		strings.Contains(command, "metadata.google.internal")
}

func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
