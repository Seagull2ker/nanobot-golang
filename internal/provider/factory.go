package provider

import (
	"fmt"

	"github.com/Seagull2ker/nanobot-go/internal/config"
)

// BuildChatModel creates a ChatModelAdapter for the given provider spec and config.
func BuildChatModel(spec ProviderSpec, cfg config.ProviderConfig) (ChatModelAdapter, error) {
	switch spec.Backend {
	case BackendOpenAICompat:
		return newOpenAICompatAdapter(spec, cfg)
	case BackendAnthropic:
		return newAnthropicAdapter(spec, cfg)
	default:
		return nil, fmt.Errorf("provider: unknown backend type %s", spec.Backend)
	}
}

// BuildChatModelFromPreset builds a ChatModelAdapter from the agent defaults config.
// presetName is ignored (no more presets); uses cfg.Agent directly.
func BuildChatModelFromPreset(presetName string, cfg *config.Config) (ChatModelAdapter, error) {
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
	return BuildChatModel(spec, providerCfg)
}
