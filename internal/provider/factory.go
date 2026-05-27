package provider

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/model"

	"github.com/Seagull2ker/nanobot-go/internal/config"
)

// chatModelWrapper wraps a model.ChatModel to satisfy ChatModelAdapter
// by adding default model name and thinking support metadata.
type chatModelWrapper struct {
	model.ChatModel
	defaultModel     string
	supportsThinking bool
}

func (w *chatModelWrapper) GetDefaultModel() string {
	return w.defaultModel
}

func (w *chatModelWrapper) SupportsThinking() bool {
	return w.supportsThinking
}

// Ensure chatModelWrapper satisfies ChatModelAdapter.
var _ ChatModelAdapter = (*chatModelWrapper)(nil)

// BuildChatModel creates a ChatModelAdapter for the given provider spec and config.
func BuildChatModel(ctx context.Context, spec ProviderSpec, cfg config.ProviderConfig) (ChatModelAdapter, error) {
	apiBase := firstNonEmpty(cfg.APIBase, spec.DefaultAPIBase)
	apiKey := cfg.APIKey

	chatModel, err := NewChatModel(ctx, ModelConfig{
		Type:    spec.Type,
		BaseURL: apiBase,
		APIKey:  apiKey,
		Model:   spec.DefaultModel,
	})
	if err != nil {
		return nil, fmt.Errorf("provider %s: %w", spec.Name, err)
	}

	return &chatModelWrapper{
		ChatModel:        chatModel,
		defaultModel:     spec.DefaultModel,
		supportsThinking: spec.SupportsThinking,
	}, nil
}

// BuildChatModelFromPreset builds a ChatModelAdapter from the agent defaults config.
func BuildChatModelFromPreset(ctx context.Context, cfg *config.Config) (ChatModelAdapter, error) {
	if cfg == nil {
		return nil, fmt.Errorf("provider: config is nil")
	}

	registry := DefaultRegistry()

	providerName := cfg.Agent.Provider
	if providerName == "" || providerName == "auto" {
		spec, err := registry.Match(cfg.Agent.Model)
		if err != nil {
			return nil, fmt.Errorf("provider: auto-match for model %s: %w", cfg.Agent.Model, err)
		}
		providerName = spec.Name
	}

	spec, err := registry.Get(providerName)
	if err != nil {
		return nil, err
	}

	providerCfg := cfg.Providers[providerName]
	return BuildChatModel(ctx, spec, providerCfg)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
