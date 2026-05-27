package tool

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
	"github.com/Seagull2ker/nanobot-go/internal/types"
)

var logApp = slog.With("module", "tool")

// Result is the return value of a tool execution.
type Result struct {
	Content string
	Error   error
}

// Tool is the interface all tools must implement.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any // JSON Schema object
	Execute(ctx context.Context, params map[string]any) (*Result, error)
	ReadOnly() bool
	ConcurrencySafe() bool
	Exclusive() bool
}

func NewProgressHandler(messageBus *bus.MessageBus) ProgressFunc {
	return func(ctx context.Context, toolName, status string) {
		pi := GetProgressInfo(ctx)
		if pi == nil {
			logApp.Debug("Progress event without channel context",
				"tool", toolName, "status", status)
			return
		}
		messageBus.PublishOutbound(ctx, &types.OutboundMessage{
			Channel:  pi.Channel,
			ChatID:   pi.ChatID,
			Content:  fmt.Sprintf("🔧 %s: %s", toolName, status),
			Metadata: map[string]any{"_progress": true},
		})
	}
}
