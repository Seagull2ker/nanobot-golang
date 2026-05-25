package tool

import (
	"context"
	"strings"
	"testing"
)

type mockTool struct {
	name      string
	result    *Result
	err       error
	readOnly  bool
	exclusive bool
}

func (m *mockTool) Name() string               { return m.name }
func (m *mockTool) Description() string        { return "mock" }
func (m *mockTool) Parameters() map[string]any { return nil }
func (m *mockTool) ReadOnly() bool             { return m.readOnly }
func (m *mockTool) ConcurrencySafe() bool      { return true }
func (m *mockTool) Exclusive() bool            { return m.exclusive }
func (m *mockTool) Execute(ctx context.Context, params map[string]any) (*Result, error) {
	return m.result, m.err
}

func TestWrapToolPassthrough(t *testing.T) {
	inner := &mockTool{
		name:   "test_tool",
		result: &Result{Content: "hello world"},
	}
	w := WrapTool(inner, 0, nil)

	if w.Name() != "test_tool" {
		t.Errorf("name: got %s", w.Name())
	}
	if w.Description() != "mock" {
		t.Error("description not passed through")
	}

	result, err := w.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "hello world" {
		t.Errorf("content: got %s", result.Content)
	}
}

func TestWrapToolErrorNormalization(t *testing.T) {
	inner := &mockTool{
		name: "failing_tool",
		result: &Result{
			Content: "Error: something went wrong",
		},
	}
	w := WrapTool(inner, 0, nil)

	result, _ := w.Execute(context.Background(), nil)
	if !strings.Contains(result.Content, toolFailureHint) {
		t.Error("error result should contain failure hint")
	}
}

func TestWrapToolGoErrorNormalization(t *testing.T) {
	inner := &mockTool{
		name: "crash_tool",
		err:  context.DeadlineExceeded,
	}
	w := WrapTool(inner, 0, nil)

	result, _ := w.Execute(context.Background(), nil)
	if result == nil {
		t.Fatal("result should not be nil")
	}
	if !strings.Contains(result.Content, "Error executing") {
		t.Errorf("error not normalized: %s", result.Content)
	}
}

func TestWrapToolTruncation(t *testing.T) {
	longContent := strings.Repeat("abcdefghij", 2000) // 20000 chars
	inner := &mockTool{
		name:   "verbose_tool",
		result: &Result{Content: longContent},
	}
	w := WrapTool(inner, 1000, nil)

	result, _ := w.Execute(context.Background(), nil)
	if len(result.Content) > 1050 {
		t.Errorf("content not truncated: %d chars", len(result.Content))
	}
	if !strings.Contains(result.Content, "truncated") {
		t.Error("truncation notice missing")
	}
}

func TestWrapToolProgressCallback(t *testing.T) {
	var events []string
	onProgress := func(ctx context.Context, toolName, status string) {
		events = append(events, toolName+":"+status)
	}

	inner := &mockTool{
		name:   "progress_tool",
		result: &Result{Content: "done"},
	}
	w := WrapTool(inner, 0, onProgress)
	w.Execute(context.Background(), nil)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(events), events)
	}
	if events[0] != "progress_tool:running" {
		t.Errorf("first event: %s", events[0])
	}
	if events[1] != "progress_tool:completed" {
		t.Errorf("second event: %s", events[1])
	}
}

func TestWrapAllTools(t *testing.T) {
	tools := []Tool{
		&mockTool{name: "a", result: &Result{Content: "a"}},
		&mockTool{name: "b", result: &Result{Content: "b"}},
	}
	wrapped := WrapAllTools(tools, 0, nil)
	if len(wrapped) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(wrapped))
	}
	if wrapped[0].Name() != "a" || wrapped[1].Name() != "b" {
		t.Error("tool order not preserved")
	}
}
