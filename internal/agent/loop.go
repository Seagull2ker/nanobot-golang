package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
	"github.com/Seagull2ker/nanobot-go/internal/errors"
	nanotool "github.com/Seagull2ker/nanobot-go/internal/tool"
	"github.com/Seagull2ker/nanobot-go/internal/types"
	"github.com/cloudwego/eino-ext/callbacks/langfuse"
	"github.com/cloudwego/eino/schema"
)

// ChatStreamer is the interface the agent loop needs.
type ChatStreamer interface {
	ChatStream(ctx context.Context, sessionID, input string) (*schema.StreamReader[*schema.Message], error)
}

// sessionQueue holds the per-session message channel.
type sessionQueue struct {
	ch chan *types.InboundMessage
}

// RunInboundLoop is the AgentLoop described in plan.md. It reads messages from
// the bus and dispatches them to per-session goroutine workers — serial within a
// session (ensuring message ordering), parallel across sessions.
//
// Each session gets a buffered channel (capacity 32). The first message for a
// new session spawns a long-lived worker; subsequent messages are queued.
// Workers shut down when the bus is closed and the queue drains.
// wg is incremented per worker so callers can Wait for graceful drain.
func RunInboundLoop(
	ctx context.Context,
	messageBus *bus.MessageBus,
	bot ChatStreamer,
	wg *sync.WaitGroup,
) {
	var sessions sync.Map

	for msg := range messageBus.ConsumeInbound() {
		key := msg.SessionKey()
		sq, loaded := sessions.LoadOrStore(key, &sessionQueue{
			ch: make(chan *types.InboundMessage, 32),
		})
		q := sq.(*sessionQueue)

		if !loaded {
			wg.Add(1)
			go func(q *sessionQueue) {
				defer wg.Done()
				for m := range q.ch {
					slog.Info("message received", "session", key, "channel", m.Channel, "preview", previewRunes(m.Content, 80))
					processMessage(ctx, messageBus, bot, m)
				}
			}(q)
		}

		select {
		case q.ch <- msg:
		case <-ctx.Done():
			return
		}
	}

	sessions.Range(func(_, v any) bool {
		close(v.(*sessionQueue).ch)
		return true
	})
}

// processMessage handles a single inbound message end-to-end:
// langfuse tracing setup → Eino ReAct streaming → consume chunks →
// publish outbound response (unless the tool already sent a reply).
// On transient errors it retries once after a 2s delay.
func processMessage(
	ctx context.Context,
	messageBus *bus.MessageBus,
	bot ChatStreamer,
	m *types.InboundMessage,
) {
	sessionID := m.SessionKey()

	targetChannel, targetChatID := m.Channel, m.ChatID
	if m.Channel == "system" {
		targetChannel, targetChatID = decodeSystemRoute(m.ChatID)
	}

	ctx = langfuse.SetTrace(ctx,
		langfuse.WithSessionID(sessionID),
		langfuse.WithUserID(m.SenderID),
		langfuse.WithName("chat"),
		langfuse.WithMetadata(map[string]string{
			"channel": targetChannel,
			"chat_id": targetChatID,
		}),
	)

	taskCtx, turnCtx := nanotool.NewTurnContext(ctx)
	taskCtx = nanotool.ContextWithSessionID(taskCtx, sessionID)
	taskCtx = nanotool.ContextWithProgressInfo(taskCtx, targetChannel, targetChatID)
	if m.Channel == "system" && m.SenderID == "subagent" {
		taskCtx = nanotool.ContextWithInputRole(taskCtx, "assistant")
	}

	reader, err := bot.ChatStream(taskCtx, sessionID, m.Content)
	err = errors.Normalize("agent.ChatStream", err)
	if err != nil && errors.Retryable(err) {
		slog.Warn("transient error, retrying", "session", sessionID, "error", err)
		time.Sleep(2 * time.Second)
		reader, err = bot.ChatStream(taskCtx, sessionID, m.Content)
		err = errors.Normalize("agent.ChatStream", err)
	}
	if err != nil {
		slog.Error("chat failed", "session", sessionID, "error", err)
		messageBus.PublishOutbound(ctx, &types.OutboundMessage{
			Channel:  targetChannel,
			ChatID:   targetChatID,
			Content:  errors.PublicMessage(err),
			Metadata: m.Metadata,
		})
		return
	}
	defer reader.Close()

	var content strings.Builder
	streamFailed := false
	for {
		chunk, recvErr := reader.Recv()
		if recvErr != nil {
			streamFailed = true
			slog.Error("stream failed", "session", sessionID, "error", recvErr)
			break
		}
		if chunk != nil {
			content.WriteString(chunk.Content)
		}
	}

	if streamFailed && content.Len() == 0 && !turnCtx.WasMessageSent() {
		messageBus.PublishOutbound(ctx, &types.OutboundMessage{
			Channel:  targetChannel,
			ChatID:   targetChatID,
			Content:  "Sorry, something went wrong.",
			Metadata: m.Metadata,
		})
		return
	}
	if content.Len() == 0 || turnCtx.WasMessageSent() {
		return
	}

	replyTo := types.ExtractReplyTo(m.Metadata)
	slog.Info("publishing outbound", "session", sessionID, "channel", targetChannel, "len", content.Len())
	messageBus.PublishOutbound(ctx, &types.OutboundMessage{
		Channel:  targetChannel,
		ChatID:   targetChatID,
		Content:  content.String(),
		ReplyTo:  replyTo,
		Metadata: m.Metadata,
	})
}

func decodeSystemRoute(chatID string) (channel, targetChatID string) {
	if strings.Contains(chatID, ":") {
		parts := strings.SplitN(chatID, ":", 2)
		return parts[0], parts[1]
	}
	return "cli", chatID
}

func previewRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
