package bus

import (
	"context"
	"log/slog"
	"sync"

	"github.com/Seagull2ker/nanobot-go/internal/types"
)

var logBus = slog.With("module", "bus")

// MessageBus decouples channels from the agent core via buffered channels.
type MessageBus struct {
	inbound      chan *types.InboundMessage
	outbound     chan *types.OutboundMessage
	inboundOnce  sync.Once
	outboundOnce sync.Once
}

// NewMessageBus creates a MessageBus with buffered channels.
func NewMessageBus() *MessageBus {
	return &MessageBus{
		inbound:  make(chan *types.InboundMessage, 100),
		outbound: make(chan *types.OutboundMessage, 100),
	}
}

// Close closes the inbound channel.
func (b *MessageBus) Close() {
	b.inboundOnce.Do(func() {
		close(b.inbound)
	})
}

// CloseOutbound closes the outbound channel.
func (b *MessageBus) CloseOutbound() {
	b.outboundOnce.Do(func() {
		close(b.outbound)
	})
}

// PublishInbound enqueues a message on the inbound channel.
func (b *MessageBus) PublishInbound(ctx context.Context, msg *types.InboundMessage) {
	defer func() {
		if r := recover(); r != nil {
			logBus.Error("publish dropped", "direction", "inbound", "panic", r)
		}
	}()
	select {
	case b.inbound <- msg:
	case <-ctx.Done():
	}
}

// ConsumeInbound returns the receive end of the inbound channel.
func (b *MessageBus) ConsumeInbound() <-chan *types.InboundMessage {
	return b.inbound
}

// PublishOutbound enqueues a message on the outbound channel.
func (b *MessageBus) PublishOutbound(ctx context.Context, msg *types.OutboundMessage) {
	defer func() {
		if r := recover(); r != nil {
			logBus.Error("publish dropped", "direction", "outbound", "panic", r)
		}
	}()
	select {
	case b.outbound <- msg:
	case <-ctx.Done():
	}
}

// ConsumeOutbound returns the receive end of the outbound channel.
func (b *MessageBus) ConsumeOutbound() <-chan *types.OutboundMessage {
	return b.outbound
}
