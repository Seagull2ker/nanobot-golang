package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/deepseek"
	"github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino-ext/components/model/ollama"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino-ext/components/model/openrouter"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"google.golang.org/genai"
)

// ModelConfig is the unified configuration for creating an Eino ChatModel.
type ModelConfig struct {
	Type    string // openai, claude, deepseek, gemini, ollama, openrouter
	BaseURL string
	APIKey  string
	Model   string
}

// NewChatModel creates an Eino ChatModel based on the config type.
// Mirrors the dispatch pattern from nanobot-eino pkg/model/model.go.
func NewChatModel(ctx context.Context, cfg ModelConfig) (model.ChatModel, error) {
	switch cfg.Type {
	case "openai":
		oaiCfg := &openai.ChatModelConfig{
			APIKey: cfg.APIKey,
			Model:  cfg.Model,
		}
		if cfg.BaseURL != "" {
			oaiCfg.BaseURL = cfg.BaseURL
			base := strings.ToLower(strings.TrimSpace(cfg.BaseURL))
			if strings.Contains(base, ".openai.azure.com") {
				oaiCfg.ByAzure = true
				oaiCfg.APIVersion = "2024-08-01-preview"
			}
		}
		return openai.NewChatModel(ctx, oaiCfg)

	case "claude":
		return claude.NewChatModel(ctx, &claude.Config{
			APIKey:    cfg.APIKey,
			Model:     cfg.Model,
			MaxTokens: 8192,
			BaseURL:   nilIfEmpty(cfg.BaseURL),
		})

	case "deepseek":
		dsCfg := &deepseek.ChatModelConfig{
			APIKey: cfg.APIKey,
			Model:  cfg.Model,
		}
		if cfg.BaseURL != "" {
			dsCfg.BaseURL = cfg.BaseURL
		}
		return deepseek.NewChatModel(ctx, dsCfg)

	case "gemini":
		client, err := genai.NewClient(ctx, &genai.ClientConfig{
			APIKey: cfg.APIKey,
		})
		if err != nil {
			return nil, fmt.Errorf("create google genai client: %w", err)
		}
		return gemini.NewChatModel(ctx, &gemini.Config{
			Client: client,
			Model:  cfg.Model,
		})

	case "ollama":
		return ollama.NewChatModel(ctx, &ollama.ChatModelConfig{
			BaseURL: cfg.BaseURL,
			Model:   cfg.Model,
		})

	case "openrouter":
		orCfg := &openrouter.Config{
			APIKey: cfg.APIKey,
			Model:  cfg.Model,
		}
		if cfg.BaseURL != "" {
			orCfg.BaseURL = cfg.BaseURL
		}
		orModel, err := openrouter.NewChatModel(ctx, orCfg)
		if err != nil {
			return nil, err
		}
		return &openRouterCompat{inner: orModel}, nil

	default:
		return nil, fmt.Errorf("unsupported model type: %s", cfg.Type)
	}
}

func nilIfEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// openRouterCompat bridges openrouter.ChatModel's missing BindTools method.
// openrouter.ChatModel implements ToolCallingChatModel (WithTools) but not the
// deprecated BindTools required by model.ChatModel.
type openRouterCompat struct {
	inner *openrouter.ChatModel
}

func (o *openRouterCompat) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return o.inner.Generate(ctx, input, opts...)
}

func (o *openRouterCompat) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return o.inner.Stream(ctx, input, opts...)
}

func (o *openRouterCompat) BindTools(tools []*schema.ToolInfo) error {
	next, err := o.inner.WithTools(tools)
	if err != nil {
		return err
	}
	typed, ok := next.(*openrouter.ChatModel)
	if !ok {
		return fmt.Errorf("openrouter WithTools returned unexpected type %T", next)
	}
	o.inner = typed
	return nil
}
