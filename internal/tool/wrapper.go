package tool

import (
	"context"
	"fmt"
	"strings"
)

// ToolResultMaxChars is the default character limit for tool results.
// Longer results are truncated from the middle to preserve context at both ends.
const ToolResultMaxChars = 16000

// toolFailureHint is appended to error results to guide the LLM toward recovery.
const toolFailureHint = "\n\n[Analyze the error above and try a different approach.]"

// ProgressFunc receives tool execution lifecycle events.
// status is "running", "completed", or "failed".
// ProgressFunc is called when a tool starts or finishes execution.
// The context carries session ID and progress info for routing.
type ProgressFunc func(ctx context.Context, toolName, status string)

// wrappedTool decorates a Tool with result truncation, error normalization,
// and progress reporting.
type wrappedTool struct {
	inner      Tool
	maxChars   int
	onProgress ProgressFunc
}

// WrapTool returns a Tool that applies result normalization to the inner tool.
func WrapTool(inner Tool, maxChars int, onProgress ProgressFunc) Tool {
	if maxChars <= 0 {
		maxChars = ToolResultMaxChars
	}
	return &wrappedTool{
		inner:      inner,
		maxChars:   maxChars,
		onProgress: onProgress,
	}
}

// WrapAllTools wraps every tool in the list with result normalization.
func WrapAllTools(tools []Tool, maxChars int, onProgress ProgressFunc) []Tool {
	wrapped := make([]Tool, len(tools))
	for i, t := range tools {
		wrapped[i] = WrapTool(t, maxChars, onProgress)
	}
	return wrapped
}

func (w *wrappedTool) Name() string               { return w.inner.Name() }
func (w *wrappedTool) Description() string        { return w.inner.Description() }
func (w *wrappedTool) Parameters() map[string]any { return w.inner.Parameters() }
func (w *wrappedTool) ReadOnly() bool             { return w.inner.ReadOnly() }
func (w *wrappedTool) ConcurrencySafe() bool      { return w.inner.ConcurrencySafe() }
func (w *wrappedTool) Exclusive() bool            { return w.inner.Exclusive() }

func (w *wrappedTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	name := w.Name()

	if w.onProgress != nil {
		w.onProgress(ctx, name, "running")
	}

	result, err := w.inner.Execute(ctx, params)

	failed := err != nil || isToolError(result)
	if w.onProgress != nil {
		status := "completed"
		if failed {
			status = "failed"
		}
		w.onProgress(ctx, name, status)
	}

	// Normalize error into result text so agent can continue.
	if err != nil {
		result = &Result{
			Content: fmt.Sprintf("Error executing %s: %v", name, err),
		}
	}
	if result == nil {
		result = &Result{Content: ""}
	}

	// Append recovery hint to error results.
	if isToolError(result) {
		result.Content += toolFailureHint
	}

	// Truncate long results from the middle.
	result.Content = truncateMiddle(result.Content, w.maxChars)

	return result, nil
}

func isToolError(r *Result) bool {
	if r == nil {
		return false
	}
	return strings.HasPrefix(r.Content, "Error") || strings.HasPrefix(r.Content, "Error:")
}

// truncateMiddle shortens text by removing the middle portion, preserving
// head and tail so context at both ends is retained.
func truncateMiddle(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	head := maxChars * 3 / 4
	tail := maxChars - head
	return text[:head] +
		fmt.Sprintf("\n\n[...%d characters truncated...]\n\n", len(text)-maxChars) +
		text[len(text)-tail:]
}
