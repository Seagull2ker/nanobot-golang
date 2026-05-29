package agent

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// IterationState holds per-iteration context for hooks.
type IterationState struct {
	Iteration   int
	Messages    []*schema.Message
	Response    string
	ToolCalls   []ToolCallInfo
	ToolResults []ToolResultInfo
}

// ToolCallInfo describes a tool call in progress.
type ToolCallInfo struct {
	ID   string
	Name string
	Args map[string]any
}

// ToolResultInfo describes a completed tool call result.
type ToolResultInfo struct {
	ID      string
	Name    string
	Content string
	Error   string
}

// AgentHook provides lifecycle callbacks into the agent execution loop.
// Hooks observe (and can modify) each iteration: before/after the LLM call,
// tool execution, streaming deltas, reasoning extraction, and final content.
//
// CompositeHook fans out to multiple hooks with error isolation — a failure
// in one hook never blocks the others or the main agent flow.
type AgentHook interface {
	BeforeIteration(ctx context.Context, state *IterationState) error
	AfterIteration(ctx context.Context, state *IterationState) error
	OnStream(ctx context.Context, delta string) error
	OnStreamEnd(ctx context.Context, resuming bool) error
	BeforeExecuteTools(ctx context.Context, calls []ToolCallInfo) error
	EmitReasoning(ctx context.Context, delta string) error
	EmitReasoningEnd(ctx context.Context) error
	FinalizeContent(ctx context.Context, content string) (string, error)
}

// BaseHook provides no-op defaults for AgentHook.
type BaseHook struct{}

func (h *BaseHook) BeforeIteration(ctx context.Context, state *IterationState) error   { return nil }
func (h *BaseHook) AfterIteration(ctx context.Context, state *IterationState) error    { return nil }
func (h *BaseHook) OnStream(ctx context.Context, delta string) error                   { return nil }
func (h *BaseHook) OnStreamEnd(ctx context.Context, resuming bool) error               { return nil }
func (h *BaseHook) BeforeExecuteTools(ctx context.Context, calls []ToolCallInfo) error { return nil }
func (h *BaseHook) EmitReasoning(ctx context.Context, delta string) error              { return nil }
func (h *BaseHook) EmitReasoningEnd(ctx context.Context) error                         { return nil }
func (h *BaseHook) FinalizeContent(ctx context.Context, content string) (string, error) {
	return content, nil
}

// CompositeHook fans out lifecycle events to all registered hooks.
// Each hook runs independently — panics and errors in one hook do not
// affect execution of the others.
type CompositeHook struct {
	mu    sync.RWMutex
	hooks []AgentHook
}

// NewCompositeHook creates a CompositeHook.
func NewCompositeHook(hooks ...AgentHook) *CompositeHook {
	return &CompositeHook{hooks: hooks}
}

// Add appends a hook.
func (c *CompositeHook) Add(h AgentHook) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks = append(c.hooks, h)
}

func (c *CompositeHook) BeforeIteration(ctx context.Context, state *IterationState) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, h := range c.hooks {
		_ = h.BeforeIteration(ctx, state)
	}
	return nil
}

func (c *CompositeHook) AfterIteration(ctx context.Context, state *IterationState) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, h := range c.hooks {
		_ = h.AfterIteration(ctx, state)
	}
	return nil
}

func (c *CompositeHook) FinalizeContent(ctx context.Context, content string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, h := range c.hooks {
		content, _ = h.FinalizeContent(ctx, content)
	}
	return content, nil
}
