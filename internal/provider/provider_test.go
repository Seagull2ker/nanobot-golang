package provider

import (
	"testing"

	"github.com/Seagull2ker/nanobot-go/internal/config"
)

func TestDefaultRegistryHasCoreProviders(t *testing.T) {
	r := DefaultRegistry()

	names := []string{"openai", "anthropic", "deepseek", "dashscope", "openrouter", "groq", "gemini", "ollama", "siliconflow", "zhipu"}
	for _, name := range names {
		if _, err := r.Get(name); err != nil {
			t.Errorf("default registry missing provider %s", name)
		}
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := DefaultRegistry()
	_, err := r.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}

func TestRegistryMatchByName(t *testing.T) {
	r := DefaultRegistry()

	spec, err := r.Match("openai")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Name != "openai" {
		t.Errorf("expected openai, got %s", spec.Name)
	}
}

func TestRegistryMatchByModelKeyword(t *testing.T) {
	r := DefaultRegistry()

	tests := []struct {
		model string
		want  string
	}{
		{"gpt-4o", "openai"},
		{"gpt-5", "openai"},
		{"claude-opus-4-5", "anthropic"},
		{"deepseek-chat", "deepseek"},
		{"qwen-max", "dashscope"},
		{"qwen-plus", "dashscope"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			spec, err := r.Match(tt.model)
			if err != nil {
				t.Fatalf("unexpected error for %s: %v", tt.model, err)
			}
			if spec.Name != tt.want {
				t.Errorf("expected %s, got %s", tt.want, spec.Name)
			}
		})
	}
}

func TestRegistryList(t *testing.T) {
	r := DefaultRegistry()
	list := r.List()
	if len(list) != 10 {
		t.Errorf("expected 10 providers, got %d", len(list))
	}
}

func TestBuildChatModelByBackend(t *testing.T) {
	r := DefaultRegistry()

	openaiSpec, _ := r.Get("openai")
	anthropicSpec, _ := r.Get("anthropic")

	cfg := config.ProviderConfig{
		APIKey: "test-key",
	}

	oa, err := BuildChatModel(openaiSpec, cfg)
	if err != nil {
		t.Fatalf("BuildChatModel(openai): %v", err)
	}
	if oa.GetDefaultModel() != "gpt-4o" {
		t.Errorf("expected gpt-4o, got %s", oa.GetDefaultModel())
	}
	if !oa.SupportsThinking() {
		t.Error("openai should support thinking")
	}

	an, err := BuildChatModel(anthropicSpec, cfg)
	if err != nil {
		t.Fatalf("BuildChatModel(anthropic): %v", err)
	}
	if an.GetDefaultModel() != "claude-opus-4-5" {
		t.Errorf("expected claude-opus-4-5, got %s", an.GetDefaultModel())
	}
	if !an.SupportsThinking() {
		t.Error("anthropic should support thinking")
	}
}

func TestBuildChatModelUnknownBackend(t *testing.T) {
	spec := ProviderSpec{
		Name:    "unknown",
		Backend: BackendType("nonexistent"),
	}
	_, err := BuildChatModel(spec, config.ProviderConfig{})
	if err == nil {
		t.Error("expected error for unknown backend")
	}
}

func TestRetryAdapterPassthrough(t *testing.T) {
	r := DefaultRegistry()
	spec, _ := r.Get("openai")
	adapter, _ := BuildChatModel(spec, config.ProviderConfig{APIKey: "test"})

	retry := WithRetry(adapter, "standard")

	if retry.GetDefaultModel() != "gpt-4o" {
		t.Error("retry adapter should pass through GetDefaultModel")
	}
	if !retry.SupportsThinking() {
		t.Error("retry adapter should pass through SupportsThinking")
	}
}

func TestFallbackAdapterPassthrough(t *testing.T) {
	r := DefaultRegistry()
	spec, _ := r.Get("openai")
	adapter, _ := BuildChatModel(spec, config.ProviderConfig{APIKey: "test"})

	fallback := WithFallback(adapter, nil, nil)

	if fallback.GetDefaultModel() != "gpt-4o" {
		t.Error("fallback adapter should pass through GetDefaultModel")
	}
}

func TestWithRetryDefaultsToStandard(t *testing.T) {
	r := DefaultRegistry()
	spec, _ := r.Get("openai")
	adapter, _ := BuildChatModel(spec, config.ProviderConfig{APIKey: "test"})

	retry := WithRetry(adapter, "bogus")
	if retry == nil {
		t.Error("retry adapter should not be nil")
	}
}
