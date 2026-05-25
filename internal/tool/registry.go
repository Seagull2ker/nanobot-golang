package tool

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Registry manages tool registration, lookup, and execution.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	cache []map[string]any
	dirty bool
}

// globalRegistry is the singleton Registry for init()-based self-registration.
var globalRegistry = &Registry{
	tools: make(map[string]Tool),
	dirty: true,
}

// Register adds a tool to the global registry.
func Register(t Tool) {
	globalRegistry.Register(t)
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool), dirty: true}
}

// Register adds a tool to this registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
	r.dirty = true
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool: %s not found", name)
	}
	return t, nil
}

// Has reports whether a tool is registered.
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

// List returns all registered tool names in sorted order.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var names []string
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Definitions returns OpenAI-compatible function definitions for all tools.
func (r *Registry) Definitions() []map[string]any {
	r.mu.RLock()
	if !r.dirty && r.cache != nil {
		defer r.mu.RUnlock()
		return r.cache
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cache != nil && !r.dirty {
		return r.cache
	}

	var names []string
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	var defs []map[string]any
	for _, name := range names {
		t := r.tools[name]
		defs = append(defs, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"parameters":  t.Parameters(),
			},
		})
	}
	r.cache = defs
	r.dirty = false
	return defs
}

// PrepareCall resolves, casts, and validates parameters for a tool call.
func (r *Registry) PrepareCall(name string, params map[string]any) (Tool, map[string]any, error) {
	t, err := r.Get(name)
	if err != nil {
		return nil, nil, err
	}
	return t, params, nil
}

// Execute runs a tool by name with the given parameters.
func (r *Registry) Execute(ctx context.Context, name string, params map[string]any) (*Result, error) {
	t, casted, err := r.PrepareCall(name, params)
	if err != nil {
		return nil, err
	}
	return t.Execute(ctx, casted)
}

// Global returns the global singleton registry.
func Global() *Registry {
	return globalRegistry
}
