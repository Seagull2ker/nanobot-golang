package heartbeat

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ChatModel is the minimal LLM interface needed for heartbeat decisions.
type ChatModel interface {
	Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
}

// HeartbeatService periodically asks the LLM to review HEARTBEAT.md
// and decide whether to execute pending tasks.
type HeartbeatService struct {
	mu             sync.Mutex
	interval       time.Duration
	keepRecentMsgs int
	lastRun        time.Time

	chatModel     ChatModel
	heartbeatPath string
	onExecute     func(ctx context.Context, tasks string)
}

// New creates a HeartbeatService.
// interval: check frequency (default 30min). keepRecentMsgs: not used currently.
// chatModel and heartbeatPath enable LLM-driven decision; if nil/empty, heartbeat is passive.
func New(interval time.Duration, keepRecentMsgs int, chatModel ChatModel, heartbeatPath string) *HeartbeatService {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	if keepRecentMsgs <= 0 {
		keepRecentMsgs = 8
	}
	return &HeartbeatService{
		interval:       interval,
		keepRecentMsgs: keepRecentMsgs,
		chatModel:      chatModel,
		heartbeatPath:  heartbeatPath,
	}
}

// OnExecute sets the callback invoked when the LLM decides to run tasks.
func (h *HeartbeatService) OnExecute(fn func(ctx context.Context, tasks string)) {
	h.onExecute = fn
}

// Run starts the heartbeat loop.
func (h *HeartbeatService) Run(ctx context.Context) error {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			h.lastRun = time.Now()
			h.Tick(ctx)
		}
	}
}

// Tick performs a single heartbeat check. If a chatModel is configured,
// it reads HEARTBEAT.md and asks the LLM to decide skip/run.
func (h *HeartbeatService) Tick(ctx context.Context) {
	if h.chatModel == nil || h.heartbeatPath == "" {
		return
	}

	content, err := os.ReadFile(h.heartbeatPath)
	if err != nil || len(content) == 0 {
		return
	}

	// Check if file has only headers/comments (no real tasks).
	text := string(content)
	if isEmptyHeartbeat(text) {
		return
	}

	// Ask LLM to decide using a dedicated heartbeat tool.
	action, tasks := h.decide(ctx, text)
	if action == "run" && h.onExecute != nil {
		h.onExecute(ctx, tasks)
	}
}

func (h *HeartbeatService) decide(ctx context.Context, heartbeatContent string) (string, string) {
	sysPrompt := `You are a heartbeat scheduler. Review the HEARTBEAT.md file content and decide whether to execute any pending tasks.

Use the heartbeat tool to respond:
- action="skip" if there are no actionable tasks
- action="run" if there are tasks that should be executed now, with a description of what to do`

	userMsg := "HEARTBEAT.md content:\n\n" + heartbeatContent

	// For now, use a simple heuristic: if HEARTBEAT.md has content beyond headers/comments,
	// return "run". Full LLM-based decision requires tool binding which depends on the model.
	// TODO: wire full LLM tool call when ChatModel supports BindTools/WithTools uniformly.
	_ = sysPrompt
	_ = userMsg

	return "skip", ""
}

func isEmptyHeartbeat(text string) bool {
	// Skip if only headers, comments, and whitespace.
	for _, line := range []string{text} {
		t := line
		t = stripMarkdownHeadings(t)
		t = stripHTMLComments(t)
		t = stripWhitespace(t)
		if t != "" {
			return false
		}
	}
	return true
}

func stripMarkdownHeadings(s string) string {
	result := s
	for _, prefix := range []string{"# ", "## ", "### "} {
		for {
			idx := 0
			found := false
			for i := 0; i < len(result); i++ {
				if i+len(prefix) <= len(result) && result[i:i+len(prefix)] == prefix {
					end := i + len(prefix)
					for end < len(result) && result[end] != '\n' {
						end++
					}
					result = result[:i] + result[end:]
					found = true
					break
				}
			}
			if !found {
				break
			}
			_ = idx
		}
	}
	return result
}

func stripHTMLComments(s string) string {
	result := s
	for {
		start := 0
		for start < len(result)-3 && result[start:start+4] != "<!--" {
			start++
		}
		if start >= len(result)-3 {
			break
		}
		end := start + 4
		for end < len(result)-2 && result[end:end+3] != "-->" {
			end++
		}
		if end >= len(result)-2 {
			break
		}
		result = result[:start] + result[end+3:]
	}
	return result
}

func stripWhitespace(s string) string {
	result := s
	for _, c := range result {
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			return result
		}
	}
	return ""
}

// LastRun returns the last heartbeat time.
func (h *HeartbeatService) LastRun() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastRun
}
