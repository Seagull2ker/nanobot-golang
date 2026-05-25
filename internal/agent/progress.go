package agent

import "context"

// ProgressHook provides streaming progress callbacks for agent execution.
type ProgressHook struct {
	OnContent   func(ctx context.Context, delta string)
	OnToolStart func(ctx context.Context, name string, args map[string]any)
	OnToolEnd   func(ctx context.Context, name string, result string)
	OnReasoning func(ctx context.Context, delta string)
	OnError     func(ctx context.Context, err error)
	OnDone      func(ctx context.Context, content string)
}

// NewProgressHook creates a ProgressHook.
func NewProgressHook() *ProgressHook {
	return &ProgressHook{}
}

// NotifyContent sends a content delta.
func (h *ProgressHook) NotifyContent(ctx context.Context, delta string) {
	if h.OnContent != nil {
		h.OnContent(ctx, delta)
	}
}

// NotifyToolStart sends a tool start event.
func (h *ProgressHook) NotifyToolStart(ctx context.Context, name string, args map[string]any) {
	if h.OnToolStart != nil {
		h.OnToolStart(ctx, name, args)
	}
}

// NotifyToolEnd sends a tool end event.
func (h *ProgressHook) NotifyToolEnd(ctx context.Context, name string, result string) {
	if h.OnToolEnd != nil {
		h.OnToolEnd(ctx, name, result)
	}
}

// NotifyReasoning sends a reasoning delta.
func (h *ProgressHook) NotifyReasoning(ctx context.Context, delta string) {
	if h.OnReasoning != nil {
		h.OnReasoning(ctx, delta)
	}
}

// NotifyError sends an error event.
func (h *ProgressHook) NotifyError(ctx context.Context, err error) {
	if h.OnError != nil {
		h.OnError(ctx, err)
	}
}

// NotifyDone sends a completion event.
func (h *ProgressHook) NotifyDone(ctx context.Context, content string) {
	if h.OnDone != nil {
		h.OnDone(ctx, content)
	}
}
