package channel

import (
	"context"
	"fmt"
	"sync"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
)

// ChannelManager handles lifecycle of all registered channels.
type ChannelManager struct {
	mu       sync.RWMutex
	channels map[string]Channel
	bus      *bus.MessageBus
}

// NewChannelManager creates a Channel ChannelManager.
func NewChannelManager(b *bus.MessageBus) *ChannelManager {
	return &ChannelManager{
		channels: make(map[string]Channel),
		bus:      b,
	}
}

// Register adds a channel to the manager.
func (m *ChannelManager) Register(ch Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[ch.Name()] = ch
}

// Get returns a channel by name.
func (m *ChannelManager) Get(name string) (Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[name]
	if !ok {
		return nil, fmt.Errorf("channel: %s not found", name)
	}
	return ch, nil
}

// StartAll starts all registered channels.
func (m *ChannelManager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for name, ch := range m.channels {
		if err := ch.Start(ctx, m.bus); err != nil {
			return fmt.Errorf("channel %s: start: %w", name, err)
		}
	}
	return nil
}

// StopAll gracefully stops all channels.
func (m *ChannelManager) StopAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for name, ch := range m.channels {
		if err := ch.Stop(ctx); err != nil {
			return fmt.Errorf("channel %s: stop: %w", name, err)
		}
	}
	return nil
}

// List returns all registered channel names.
func (m *ChannelManager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var names []string
	for name := range m.channels {
		names = append(names, name)
	}
	return names
}
