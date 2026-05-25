package provider

import (
	"fmt"
	"strings"
)

// BackendType identifies which adapter implementation to use.
type BackendType string

const (
	BackendOpenAICompat BackendType = "openai_compat"
	BackendAnthropic    BackendType = "anthropic"
)

// ThinkingStyle defines how thinking/reasoning is injected into API requests.
type ThinkingStyle string

const (
	ThinkingNone           ThinkingStyle = ""
	ThinkingType           ThinkingStyle = "thinking_type"
	ThinkingEnabled        ThinkingStyle = "enable_thinking"
	ThinkingReasoningSplit ThinkingStyle = "reasoning_split"
)

// ModelOverride allows per-model parameter overrides.
type ModelOverride struct {
	Temperature     *float64
	MaxTokens       *int
	ReasoningEffort *string
}

// ProviderSpec describes a single LLM provider in the registry.
type ProviderSpec struct {
	Name             string
	Backend          BackendType
	Keywords         []string
	EnvKey           string
	DefaultModel     string
	DefaultAPIBase   string
	Models           []string
	SupportsThinking bool
	ThinkingStyle    ThinkingStyle
	IsGateway        bool
	StripModelPrefix bool
	ModelOverrides   map[string]ModelOverride
}

// Registry manages all registered ProviderSpecs.
type Registry struct {
	byName  map[string]ProviderSpec
	byModel map[string]string
	all     []ProviderSpec
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		byName:  make(map[string]ProviderSpec),
		byModel: make(map[string]string),
	}
}

// Register adds a provider spec to the registry.
func (r *Registry) Register(spec ProviderSpec) {
	r.byName[spec.Name] = spec
	r.all = append(r.all, spec)

	lower := strings.ToLower(spec.Name)
	r.byModel[lower] = spec.Name

	for _, kw := range spec.Keywords {
		r.byModel[strings.ToLower(kw)] = spec.Name
	}
	for _, m := range spec.Models {
		r.byModel[strings.ToLower(m)] = spec.Name
	}
}

// Get returns the ProviderSpec for a named provider.
func (r *Registry) Get(name string) (ProviderSpec, error) {
	spec, ok := r.byName[name]
	if !ok {
		return ProviderSpec{}, fmt.Errorf("provider: %s not found", name)
	}
	return spec, nil
}

// Match finds a provider that can serve the given model name.
func (r *Registry) Match(model string) (ProviderSpec, error) {
	if spec, ok := r.byName[model]; ok {
		return spec, nil
	}

	key := strings.ToLower(model)
	if name, ok := r.byModel[key]; ok {
		return r.byName[name], nil
	}

	for keyword, name := range r.byModel {
		if strings.HasPrefix(key, keyword) || strings.HasPrefix(keyword, key) {
			return r.byName[name], nil
		}
	}

	return ProviderSpec{}, fmt.Errorf("provider: no match for model %s", model)
}

// List returns all registered provider specs in registration order.
func (r *Registry) List() []ProviderSpec {
	return append([]ProviderSpec(nil), r.all...)
}

// DefaultRegistry returns a Registry pre-populated with 10 supported providers.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(ProviderSpec{
		Name: "openai", Backend: BackendOpenAICompat,
		Keywords: []string{"openai", "gpt-4", "gpt-5", "o1", "o3", "o4"},
		EnvKey:   "OPENAI_API_KEY", DefaultModel: "gpt-4o", DefaultAPIBase: "https://api.openai.com/v1",
		Models:           []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1", "o1", "o3", "o4-mini", "gpt-5"},
		SupportsThinking: true, ThinkingStyle: ThinkingReasoningSplit,
	})
	r.Register(ProviderSpec{
		Name: "anthropic", Backend: BackendAnthropic,
		Keywords: []string{"claude", "anthropic"},
		EnvKey:   "ANTHROPIC_API_KEY", DefaultModel: "claude-opus-4-5", DefaultAPIBase: "https://api.anthropic.com",
		Models:           []string{"claude-opus-4-5", "claude-sonnet-4-6", "claude-haiku-4-5"},
		SupportsThinking: true,
	})
	r.Register(ProviderSpec{
		Name: "deepseek", Backend: BackendOpenAICompat,
		Keywords: []string{"deepseek", "deepseek-chat", "deepseek-reasoner"},
		EnvKey:   "DEEPSEEK_API_KEY", DefaultModel: "deepseek-chat", DefaultAPIBase: "https://api.deepseek.com",
		Models:           []string{"deepseek-chat", "deepseek-reasoner"},
		SupportsThinking: true, ThinkingStyle: ThinkingType,
	})
	r.Register(ProviderSpec{
		Name: "dashscope", Backend: BackendOpenAICompat,
		Keywords: []string{"qwen", "dashscope", "tongyi", "qwen-plus", "qwen-max"},
		EnvKey:   "DASHSCOPE_API_KEY", DefaultModel: "qwen-max", DefaultAPIBase: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Models:           []string{"qwen-max", "qwen-plus", "qwen-turbo"},
		SupportsThinking: true, ThinkingStyle: ThinkingEnabled,
	})
	r.Register(ProviderSpec{Name: "openrouter", Backend: BackendOpenAICompat, Keywords: []string{"openrouter"}, EnvKey: "OPENROUTER_API_KEY", DefaultModel: "openai/gpt-4o", DefaultAPIBase: "https://openrouter.ai/api/v1", IsGateway: true})
	r.Register(ProviderSpec{Name: "groq", Backend: BackendOpenAICompat, Keywords: []string{"groq", "llama"}, EnvKey: "GROQ_API_KEY", DefaultModel: "llama-3.3-70b-versatile", DefaultAPIBase: "https://api.groq.com/openai/v1"})
	r.Register(ProviderSpec{Name: "gemini", Backend: BackendOpenAICompat, Keywords: []string{"gemini", "google"}, EnvKey: "GEMINI_API_KEY", DefaultModel: "gemini-2.5-pro", DefaultAPIBase: "https://generativelanguage.googleapis.com/v1beta/openai"})
	r.Register(ProviderSpec{Name: "ollama", Backend: BackendOpenAICompat, Keywords: []string{"ollama"}, DefaultModel: "llama3", DefaultAPIBase: "http://localhost:11434/v1"})
	r.Register(ProviderSpec{Name: "siliconflow", Backend: BackendOpenAICompat, Keywords: []string{"siliconflow", "silicon"}, EnvKey: "SILICONFLOW_API_KEY", DefaultModel: "deepseek-ai/DeepSeek-V3", DefaultAPIBase: "https://api.siliconflow.cn/v1"})
	r.Register(ProviderSpec{Name: "zhipu", Backend: BackendOpenAICompat, Keywords: []string{"zhipu", "glm", "chatglm"}, EnvKey: "ZHIPU_API_KEY", DefaultModel: "glm-4-plus", DefaultAPIBase: "https://open.bigmodel.cn/api/paas/v4"})
	return r
}
