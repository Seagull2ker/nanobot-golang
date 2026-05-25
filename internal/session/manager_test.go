package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestNewSession(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewSessionManager(dir)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	s := mgr.GetOrCreate("test-key")
	if s.Key != "test-key" {
		t.Errorf("key: got %s", s.Key)
	}
	if s.Messages == nil {
		t.Error("Messages should not be nil")
	}
	if s.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewSessionManager(dir)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	s := mgr.GetOrCreate("test-key")
	s.AddMessage(schema.UserMessage("hello"))
	s.AddMessage(schema.AssistantMessage("hi there", nil))
	s.LastConsolidated = 2

	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Invalidate cache and reload.
	mgr.Invalidate("test-key")
	loaded := mgr.GetOrCreate("test-key")

	if loaded.LastConsolidated != 2 {
		t.Errorf("LastConsolidated: got %d", loaded.LastConsolidated)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("Messages: got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Errorf("msg[0]: got %s", loaded.Messages[0].Content)
	}
	if loaded.Messages[1].Content != "hi there" {
		t.Errorf("msg[1]: got %s", loaded.Messages[1].Content)
	}
}

func TestGetHistoryAlignment(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewSessionManager(dir)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	s := mgr.GetOrCreate("test-key")
	// Simulate a turn: user -> assistant (tool_call) -> tool_result
	s.AddMessage(schema.UserMessage("user msg"))
	s.AddMessage(&schema.Message{Role: schema.Assistant, Content: "", ToolCalls: []schema.ToolCall{
		{ID: "call_1", Function: schema.FunctionCall{Name: "test", Arguments: "{}"}},
	}})
	s.AddMessage(&schema.Message{Role: schema.Tool, ToolCallID: "call_1", Content: "result"})
	s.AddMessage(schema.AssistantMessage("final response", nil))

	// With LastConsolidated=0, should return all from first user message.
	history := s.GetHistory(10)
	if len(history) != 4 {
		t.Fatalf("history: got %d messages", len(history))
	}
	if history[0].Role != schema.User {
		t.Error("first message should be user role")
	}
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewSessionManager(dir)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	s1 := mgr.GetOrCreate("key-1")
	s1.AddMessage(schema.UserMessage("a"))
	mgr.Save(s1)

	s2 := mgr.GetOrCreate("key-2")
	s2.AddMessage(schema.UserMessage("b"))
	mgr.Save(s2)

	keys, err := mgr.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(keys) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(keys))
	}
}

func TestSaveRoundTripWithToolCalls(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewSessionManager(dir)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	s := mgr.GetOrCreate("tool-test")
	s.AddMessage(&schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: "tc_1", Type: "function", Function: schema.FunctionCall{Name: "exec", Arguments: `{"cmd":"ls"}`}},
		},
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "tool_calls",
			Usage:        &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		},
	})

	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	mgr.Invalidate("tool-test")
	loaded := mgr.GetOrCreate("tool-test")
	if len(loaded.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(loaded.Messages))
	}
	msg := loaded.Messages[0]
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "exec" {
		t.Error("tool call not preserved")
	}
	if msg.ResponseMeta == nil || msg.ResponseMeta.Usage.TotalTokens != 120 {
		t.Error("usage not preserved")
	}
}

func TestFilePersistence(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewSessionManager(dir)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}

	s := mgr.GetOrCreate("persist-test")
	s.Metadata["user_name"] = "Alice"
	s.AddMessage(schema.UserMessage("test"))
	mgr.Save(s)

	// Verify file exists on disk.
	expectedPath := filepath.Join(dir, "persist-test.jsonl")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("file not created: %s", expectedPath)
	}
}

func TestClear(t *testing.T) {
	s := &Session{Key: "clear-test", Messages: make([]*schema.Message, 0)}
	s.AddMessage(schema.UserMessage("msg1"))
	s.AddMessage(schema.UserMessage("msg2"))
	s.LastConsolidated = 3

	s.Clear()
	if len(s.Messages) != 0 {
		t.Error("Messages should be empty after Clear")
	}
	if s.LastConsolidated != 0 {
		t.Error("LastConsolidated should be 0 after Clear")
	}
}
