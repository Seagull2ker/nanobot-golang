package channel

import (
	"context"
	"fmt"
	"sync"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
)

// Manager handles lifecycle of all registered channels.
type Manager struct {
	mu       sync.RWMutex
	channels map[string]Channel
	bus      *bus.MessageBus
}

// NewManager creates a Channel Manager.
func NewManager(b *bus.MessageBus) *Manager {
	return &Manager{
		channels: make(map[string]Channel),
		bus:      b,
	}
}

// Register adds a channel to the manager.
func (m *Manager) Register(ch Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[ch.Name()] = ch
}

// Get returns a channel by name.
func (m *Manager) Get(name string) (Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[name]
	if !ok {
		return nil, fmt.Errorf("channel: %s not found", name)
	}
	return ch, nil
}

// StartAll starts all registered channels.
func (m *Manager) StartAll(ctx context.Context) error {
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
func (m *Manager) StopAll(ctx context.Context) error {
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
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var names []string
	for name := range m.channels {
		names = append(names, name)
	}
	return names
}
