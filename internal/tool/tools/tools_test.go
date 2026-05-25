package tools

import (
	"testing"

	"github.com/Seagull2ker/nanobot-go/internal/tool"
)

func TestAllToolsRegistered(t *testing.T) {
	expected := []string{"exec", "read_file", "write_file", "edit_file", "list_dir", "web_fetch", "web_search"}

	for _, name := range expected {
		if !tool.Global().Has(name) {
			t.Errorf("tool %s not registered in global registry", name)
		}
	}

	names := tool.Global().List()
	if len(names) < 7 {
		t.Errorf("expected at least 7 tools, got %d: %v", len(names), names)
	}
}

func TestToolProperties(t *testing.T) {
	tests := []struct {
		name       string
		readOnly   bool
		exclusive  bool
	}{
		{"exec", false, true},
		{"read_file", true, false},
		{"write_file", false, false},
		{"edit_file", false, false},
		{"list_dir", true, false},
		{"web_fetch", true, false},
		{"web_search", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tl, err := tool.Global().Get(tt.name)
			if err != nil {
				t.Fatalf("get tool: %v", err)
			}
			if tl.ReadOnly() != tt.readOnly {
				t.Errorf("ReadOnly: got %v, want %v", tl.ReadOnly(), tt.readOnly)
			}
			if tl.Exclusive() != tt.exclusive {
				t.Errorf("Exclusive: got %v, want %v", tl.Exclusive(), tt.exclusive)
			}
		})
	}
}

func TestToolDefinitions(t *testing.T) {
	defs := tool.Global().Definitions()
	if len(defs) < 7 {
		t.Errorf("expected at least 7 definitions, got %d", len(defs))
	}
	for _, def := range defs {
		if def["type"] != "function" {
			t.Errorf("expected type=function, got %v", def["type"])
		}
		fn, ok := def["function"].(map[string]any)
		if !ok {
			t.Error("missing function key in definition")
			continue
		}
		if fn["name"] == nil {
			t.Error("missing name in function definition")
		}
	}
}
