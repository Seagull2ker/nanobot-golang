package channel

import (
	"context"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
	"github.com/Seagull2ker/nanobot-go/internal/types"
)

// Channel is the interface for all communication channels.
type Channel interface {
	Name() string
	Start(ctx context.Context, bus *bus.MessageBus) error
	Stop(ctx context.Context) error
	Send(ctx context.Context, msg *types.OutboundMessage) error
	SupportsStreaming() bool
}
