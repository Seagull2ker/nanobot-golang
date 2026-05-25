package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/Seagull2ker/nanobot-go/internal/session"
)

// ---------- MemoryStore ----------

// MemoryStore manages two-layer persistent memory:
//   - MEMORY.md: long-term facts, overwritten on each consolidation.
//   - HISTORY.md: append-only timestamped event log.
type MemoryStore struct {
	dir string
}

func NewMemoryStore(dir string) *MemoryStore {
	return &MemoryStore{dir: dir}
}

// ReadMemory returns MEMORY.md content ("" if missing).
func (s *MemoryStore) ReadMemory() string {
	data, err := os.ReadFile(filepath.Join(s.dir, "MEMORY.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteMemory overwrites MEMORY.md.
func (s *MemoryStore) WriteMemory(content string) error {
	return os.WriteFile(filepath.Join(s.dir, "MEMORY.md"), []byte(content), 0644)
}

// AppendHistory appends a timestamped entry to HISTORY.md.
func (s *MemoryStore) AppendHistory(entry string) error {
	f, err := os.OpenFile(filepath.Join(s.dir, "HISTORY.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	ts := time.Now().Format("[2006-01-02 15:04]")
	_, err = fmt.Fprintf(f, "%s %s\n\n", ts, entry)
	return err
}

// GetMemoryContext returns formatted memory for system prompt injection.
func (s *MemoryStore) GetMemoryContext() string {
	content := strings.TrimSpace(s.ReadMemory())
	if content == "" {
		return ""
	}
	return "## Long-term Memory\n" + content
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
	chatModel  model.BaseChatModel
	sessionMgr *session.SessionManager
	config     ConsolidationConfig
	locks      sync.Map

	consecutiveFailsMu sync.Mutex
	consecutiveFails   int
}

// NewMemoryConsolidator creates a MemoryConsolidator.
// sessionMgr is optional — when provided, sessions are saved after consolidation.
func NewMemoryConsolidator(store *MemoryStore, chatModel model.BaseChatModel, sessionMgr *session.SessionManager, cfg ConsolidationConfig) *MemoryConsolidator {
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

// Consolidate sends messages to the LLM with a save_memory tool and writes results.
// Falls back to raw archive after 3 consecutive failures.
func (c *MemoryConsolidator) Consolidate(ctx context.Context, messages []*schema.Message) error {
	prompt := schema.SystemMessage(`You are a memory consolidation system. Summarize the conversation into structured facts.

Use the save_memory tool to store:
1. Key facts about the user (preferences, name, role, projects)
2. Important decisions made
3. Context that will be useful for future conversations

Be concise. Only save genuinely important information.`)

	input := append([]*schema.Message{prompt}, messages...)

	resp, err := c.chatModel.Generate(ctx, input)
	if err != nil || resp == nil || resp.Content == "" {
		return c.rawArchive(messages)
	}

	// Write the LLM's consolidation output to HISTORY.md and update MEMORY.md.
	summary := resp.Content
	if err := c.Store.AppendHistory(summary); err != nil {
		return err
	}

	// Append summary to MEMORY.md.
	existing := c.Store.ReadMemory()
	if existing == "" {
		existing = "# Nanobot's Long-term Memory\n\n"
	}
	newContent := existing + "\n" + summary + "\n"
	if err := c.Store.WriteMemory(newContent); err != nil {
		return err
	}

	c.consecutiveFailsMu.Lock()
	c.consecutiveFails = 0
	c.consecutiveFailsMu.Unlock()

	slog.Info("memory consolidated", "messages", len(messages))
	return nil
}

// rawArchive writes messages directly to HISTORY.md as a fallback.
func (c *MemoryConsolidator) rawArchive(messages []*schema.Message) error {
	c.consecutiveFailsMu.Lock()
	c.consecutiveFails++
	fails := c.consecutiveFails
	c.consecutiveFailsMu.Unlock()

	var parts []string
	for _, m := range messages {
		if m.Content != "" {
			parts = append(parts, m.Content)
		}
	}
	entry := "[RAW ARCHIVE] " + strings.Join(parts, "\n")
	if fails >= 3 {
		entry = "[RAW ARCHIVE — LLM consolidation failed 3 consecutive times] " + entry
	}

	return c.Store.AppendHistory(entry)
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
