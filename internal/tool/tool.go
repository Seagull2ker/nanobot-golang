package tool

import "context"

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
