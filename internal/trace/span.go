package trace

import (
	"context"
	"log/slog"
)

// StartSpan creates a manual trace span for non-Eino components (e.g., memory consolidation).
// Full tracing requires Langfuse init via trace.Init().
func StartSpan(ctx context.Context, name string, input map[string]any) context.Context {
	slog.Debug("trace span start", "name", name)
	return context.WithValue(ctx, spanKey{}, &spanData{name: name, isOpen: true})
}

// EndSpan closes a manual span created by StartSpan.
func EndSpan(ctx context.Context, err error) {
	data, ok := ctx.Value(spanKey{}).(*spanData)
	if !ok || !data.isOpen {
		return
	}
	data.isOpen = false
	if err != nil {
		slog.Debug("trace span end", "name", data.name, "error", err)
	} else {
		slog.Debug("trace span end", "name", data.name)
	}
}

type spanKey struct{}

type spanData struct {
	name   string
	isOpen bool
}
