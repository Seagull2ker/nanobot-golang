package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	emodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"

	"github.com/Seagull2ker/nanobot-go/internal/session"
)

var logMemory = slog.With("module", "memory")

// SaveMemoryArgs is the structured argument for the save_memory tool call.
type SaveMemoryArgs struct {
	HistoryEntry string `json:"history_entry" jsonschema:"description=A paragraph summarizing key events/decisions/topics. Start with [YYYY-MM-DD HH:MM]. Include detail useful for grep search."`
	MemoryUpdate string `json:"memory_update" jsonschema:"description=Full updated long-term memory as markdown. Include all existing facts plus new ones. Return unchanged if nothing new."`
}

const maxFailuresBeforeRawArchive = 3

var historyPrefixRe = regexp.MustCompile(`^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}\]\s*`)

func normalizeHistoryEntryTimestamp(entry string, now time.Time) string {
	trimmed := strings.TrimSpace(entry)
	prefix := "[" + now.Format("2006-01-02 15:04") + "] "
	body := historyPrefixRe.ReplaceAllString(trimmed, "")
	return prefix + body
}

// ---------- MemoryStore ----------

// MemoryStore manages two-layer persistent memory:
//   - MEMORY.md: long-term facts, overwritten on each consolidation.
//   - HISTORY.md: append-only timestamped event log.
type MemoryStore struct {
	memoryDir           string
	memoryFile          string
	historyFile         string
	consecutiveFailures int
}

func NewMemoryStore(memoryDir string) (*MemoryStore, error) {
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		return nil, err
	}
	return &MemoryStore{
		memoryDir:   memoryDir,
		memoryFile:  filepath.Join(memoryDir, "MEMORY.md"),
		historyFile: filepath.Join(memoryDir, "HISTORY.md"),
	}, nil
}

// ReadMemory returns MEMORY.md content ("" if missing).
func (s *MemoryStore) ReadMemory() string {
	data, err := os.ReadFile(s.memoryFile)
	if err != nil {
		if !os.IsNotExist(err) {
			logMemory.Warn("Failed to read long-term memory", "path", s.memoryFile, "error", err)
		}
		return ""
	}
	return string(data)
}

// WriteMemory overwrites MEMORY.md.
func (s *MemoryStore) WriteMemory(content string) error {
	return os.WriteFile(s.memoryFile, []byte(content), 0644)
}

// AppendHistory appends a normalized entry to HISTORY.md.
// The entry is trimmed of trailing newlines and double-spaced from the next entry.
func (s *MemoryStore) AppendHistory(entry string) error {
	f, err := os.OpenFile(s.historyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimRight(entry, "\n") + "\n\n")
	return err
}

// GetMemoryContext returns formatted memory for system prompt injection.
func (s *MemoryStore) GetMemoryContext() string {
	longTerm := s.ReadMemory()
	if longTerm == "" {
		return ""
	}
	return fmt.Sprintf("## Long-term Memory\n%s", longTerm)
}

func formatMessages(messages []*schema.Message) string {
	var lines []string
	for _, msg := range messages {
		if msg.Content == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("[%s]: %s",
			strings.ToUpper(string(msg.Role)), msg.Content))
	}
	return strings.Join(lines, "\n")
}

// Consolidate uses an LLM to summarize messages into MEMORY.md + HISTORY.md.
// Returns true on success or after raw-archive fallback.
func (s *MemoryStore) Consolidate(ctx context.Context, messages []*schema.Message, chatModel emodel.ChatModel) bool {
	if len(messages) == 0 {
		return true
	}

	currentMemory := s.ReadMemory()
	displayMemory := currentMemory
	if displayMemory == "" {
		displayMemory = "(empty)"
	}

	prompt := fmt.Sprintf(`Process this conversation and call the save_memory tool with your consolidation.

## Current Long-term Memory
%s

## Conversation to Process
%s`, displayMemory, formatMessages(messages))

	chatMessages := []*schema.Message{
		schema.SystemMessage("You are a memory consolidation agent for nanobot. Call the save_memory tool with your consolidation of the conversation. You MUST use the save_memory tool."),
		schema.UserMessage(prompt),
	}

	// Create the save_memory tool (only for schema binding; we parse arguments manually).
	saveTool, err := utils.InferTool("save_memory",
		"Save the memory consolidation result to persistent storage.",
		func(_ context.Context, _ *SaveMemoryArgs) (string, error) { return "", nil })
	if err != nil {
		logMemory.Warn("Failed to create tool", "error", err)
		return s.failOrRawArchive(messages)
	}
	info, err := saveTool.Info(ctx)
	if err != nil {
		logMemory.Warn("Failed to get tool info", "error", err)
		return s.failOrRawArchive(messages)
	}

	// generateWithFallback calls Generate with tool_choice=forced first.
	// If the model signals it does not support tool_choice, retries with tool_choice=allowed.
	generateWithFallback := func(m emodel.BaseChatModel) (*schema.Message, error) {
		resp, genErr := m.Generate(ctx, chatMessages,
			emodel.WithToolChoice(schema.ToolChoiceForced))
		if genErr != nil && isToolChoiceUnsupportedError(genErr) {
			logMemory.Warn("tool_choice=forced unsupported, retrying with tool_choice=allowed")
			resp, genErr = m.Generate(ctx, chatMessages,
				emodel.WithToolChoice(schema.ToolChoiceAllowed))
		}
		return resp, genErr
	}

	var resp *schema.Message

	if tcModel, ok := chatModel.(emodel.ToolCallingChatModel); ok {
		boundModel, bindErr := tcModel.WithTools([]*schema.ToolInfo{info})
		if bindErr != nil {
			logMemory.Warn("WithTools failed", "error", bindErr)
			return s.failOrRawArchive(messages)
		}
		resp, err = generateWithFallback(boundModel)
	} else {
		if bindErr := chatModel.BindTools([]*schema.ToolInfo{info}); bindErr != nil {
			logMemory.Warn("BindTools failed", "error", bindErr)
			return s.failOrRawArchive(messages)
		}
		resp, err = generateWithFallback(chatModel)
	}

	if err != nil {
		logMemory.Warn("LLM call failed", "error", err)
		return s.failOrRawArchive(messages)
	}

	if len(resp.ToolCalls) == 0 {
		logMemory.Warn("LLM did not call save_memory", "content_len", len(resp.Content))
		return s.failOrRawArchive(messages)
	}

	for _, tc := range resp.ToolCalls {
		if tc.Function.Name != "save_memory" {
			continue
		}
		var args SaveMemoryArgs
		if jsonErr := json.Unmarshal([]byte(tc.Function.Arguments), &args); jsonErr != nil {
			logMemory.Warn("Failed to parse arguments", "error", jsonErr)
			return s.failOrRawArchive(messages)
		}

		entry := strings.TrimSpace(args.HistoryEntry)
		if entry == "" {
			logMemory.Warn("history_entry is empty")
			return s.failOrRawArchive(messages)
		}

		now := time.Now()
		normalizedEntry := normalizeHistoryEntryTimestamp(entry, now)

		if err := s.AppendHistory(normalizedEntry); err != nil {
			logMemory.Warn("Failed to append history", "error", err)
			return s.failOrRawArchive(messages)
		}

		update := args.MemoryUpdate
		if update != currentMemory {
			if err := s.WriteMemory(update); err != nil {
				logMemory.Warn("Failed to write long-term memory", "error", err)
			}
		}

		s.consecutiveFailures = 0
		logMemory.Info("Consolidation done", "messages", len(messages))
		return true
	}

	logMemory.Warn("save_memory tool call not found in response")
	return s.failOrRawArchive(messages)
}

// failOrRawArchive increments failure count; after threshold, raw-archives and returns true.
func (s *MemoryStore) failOrRawArchive(messages []*schema.Message) bool {
	s.consecutiveFailures++
	logMemory.Warn("Consolidation failed", "attempt", s.consecutiveFailures, "max", maxFailuresBeforeRawArchive)

	if s.consecutiveFailures < maxFailuresBeforeRawArchive {
		return false
	}
	s.rawArchive(messages)
	s.consecutiveFailures = 0
	return true
}

func (s *MemoryStore) rawArchive(messages []*schema.Message) {
	now := time.Now()
	ts := now.Format("2006-01-02 15:04")
	entry := fmt.Sprintf("[%s] [RAW ARCHIVE] %d messages\n%s",
		ts, len(messages), formatMessages(messages))
	if err := s.AppendHistory(entry); err != nil {
		logMemory.Warn("Raw archive also failed", "error", err)
	}
	logMemory.Warn("Degraded: raw-archived messages", "messages", len(messages))
}

// isToolChoiceUnsupportedError detects errors from models that do not support
// tool_choice parameter.
func isToolChoiceUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tool_choice") ||
		strings.Contains(msg, "does not support") ||
		strings.Contains(msg, "should be")
}

// ---------- MemoryConsolidator ----------

// ConsolidationConfig controls consolidation behavior.
type ConsolidationConfig struct {
	ContextWindowTokens    int     // model context window size
	MaxConsolidationRounds int     // max rounds per trigger (default 5)
	ConsolidationRatio     float64 // target fraction of context window (default 0.5)
}

func DefaultConsolidationConfig() ConsolidationConfig {
	return ConsolidationConfig{
		ContextWindowTokens:    65536,
		MaxConsolidationRounds: 5,
		ConsolidationRatio:     0.5,
	}
}

// MemoryConsolidator manages automatic LLM-driven memory consolidation.
type MemoryConsolidator struct {
	Store      *MemoryStore
	chatModel  emodel.ChatModel
	sessionMgr *session.SessionManager
	config     ConsolidationConfig
	locks      sync.Map
}

// NewMemoryConsolidator creates a MemoryConsolidator.
// sessionMgr is optional — when provided, sessions are saved after consolidation.
func NewMemoryConsolidator(store *MemoryStore, chatModel emodel.ChatModel, sessionMgr *session.SessionManager, cfg ConsolidationConfig) *MemoryConsolidator {
	return &MemoryConsolidator{
		Store:      store,
		chatModel:  chatModel,
		sessionMgr: sessionMgr,
		config:     cfg,
	}
}

// MaybeConsolidateByTokens checks if the session exceeds the token budget
// and consolidates if needed. Called before and after each agent turn.
func (c *MemoryConsolidator) MaybeConsolidateByTokens(ctx context.Context, s *session.Session, basePromptTokens int) error {
	lock, _ := c.locks.LoadOrStore(s.Key, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	budget := int(float64(c.config.ContextWindowTokens) * c.config.ConsolidationRatio)
	target := c.config.ContextWindowTokens - budget

	for round := 0; round < c.config.MaxConsolidationRounds; round++ {
		estimated := c.estimatePromptTokens(s, basePromptTokens)
		if estimated < target {
			return nil
		}

		tokensToRemove := estimated - target + 1024
		if tokensToRemove <= 0 {
			return nil
		}

		boundary := c.pickConsolidationBoundary(s, tokensToRemove)
		if boundary <= s.LastConsolidated {
			boundary = s.LastConsolidated + 1
			if boundary >= len(s.Messages) {
				return nil
			}
		}

		batch := s.Messages[s.LastConsolidated:boundary]
		if len(batch) == 0 {
			return nil
		}

		if err := c.Consolidate(ctx, batch); err != nil {
			return err
		}

		s.LastConsolidated = boundary
	}

	return nil
}

// ArchiveUnconsolidated consolidates all unarchived messages from the session
// into long-term memory and advances LastConsolidated. Used by /new to preserve
// context before clearing the session.
func (c *MemoryConsolidator) ArchiveUnconsolidated(ctx context.Context, s *session.Session) error {
	msgs := s.Messages[s.LastConsolidated:]
	if len(msgs) == 0 {
		return nil
	}
	if err := c.Consolidate(ctx, msgs); err != nil {
		return err
	}
	s.LastConsolidated = len(s.Messages)
	return nil
}

// Consolidate delegates to Store.Consolidate.
func (c *MemoryConsolidator) Consolidate(ctx context.Context, messages []*schema.Message) error {
	if !c.Store.Consolidate(ctx, messages, c.chatModel) {
		return fmt.Errorf("memory consolidation failed")
	}
	return nil
}

// estimatePromptTokens estimates total token usage for the session.
// Uses a simple chars/3 approximation.
func (c *MemoryConsolidator) estimatePromptTokens(s *session.Session, basePromptTokens int) int {
	total := basePromptTokens

	memCtx := c.Store.GetMemoryContext()
	total += len(memCtx) / 3

	for i := s.LastConsolidated; i < len(s.Messages); i++ {
		msg := s.Messages[i]
		total += len(msg.Content) / 3
		for _, tc := range msg.ToolCalls {
			total += len(tc.Function.Arguments) / 3
			total += len(tc.Function.Name) / 3
		}
	}

	return total
}

// pickConsolidationBoundary finds a safe user-turn boundary for truncation.
// Returns the index just before a user message (so the consolidated batch
// ends cleanly at an assistant/tool turn).
func (c *MemoryConsolidator) pickConsolidationBoundary(s *session.Session, tokensToRemove int) int {
	currentTokens := 0
	boundary := s.LastConsolidated

	for i := s.LastConsolidated; i < len(s.Messages); i++ {
		m := s.Messages[i]
		currentTokens += len(m.Content) / 3

		// Found a user turn boundary — safe to cut here.
		if currentTokens >= tokensToRemove && m.Role == schema.User {
			return i
		}
		boundary = i
	}

	// Fallback: return last processed index.
	if boundary > s.LastConsolidated {
		return boundary
	}
	return s.LastConsolidated + 1
}
