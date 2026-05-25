package provider

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/Seagull2ker/nanobot-go/internal/types"
)

// ChatModelAdapter wraps Eino's ChatModel with nanobot-specific metadata.
// Extends model.ChatModel (which adds BindTools) for Eino ReAct agent compatibility.
type ChatModelAdapter interface {
	model.ChatModel

	GetDefaultModel() string
	SupportsThinking() bool
}

// ChatOption configures a Chat/ChatStream call.
type ChatOption func(*chatConfig)

type chatConfig struct {
	model           string
	maxTokens       int
	temperature     float32
	reasoningEffort string
	tools           []*schema.ToolInfo
	toolChoice      *schema.ToolChoice
}

// WithModel sets the model name for a chat call.
func WithModel(name string) ChatOption {
	return func(c *chatConfig) { c.model = name }
}

// WithMaxTokens sets max tokens for a chat call.
func WithMaxTokens(n int) ChatOption {
	return func(c *chatConfig) { c.maxTokens = n }
}

// WithTemperature sets the temperature for a chat call.
func WithTemperature(t float32) ChatOption {
	return func(c *chatConfig) { c.temperature = t }
}

// WithReasoningEffort sets the reasoning effort for a chat call.
func WithReasoningEffort(e string) ChatOption {
	return func(c *chatConfig) { c.reasoningEffort = e }
}

// WithTools sets the tools for a chat call.
func WithTools(tools []*schema.ToolInfo) ChatOption {
	return func(c *chatConfig) { c.tools = tools }
}

// WithToolChoice sets the tool choice for a chat call.
func WithToolChoice(choice schema.ToolChoice) ChatOption {
	return func(c *chatConfig) { c.toolChoice = &choice }
}

func (c *chatConfig) toModelOptions() []model.Option {
	var opts []model.Option
	if c.model != "" {
		opts = append(opts, model.WithModel(c.model))
	}
	if c.maxTokens > 0 {
		opts = append(opts, model.WithMaxTokens(c.maxTokens))
	}
	if c.temperature > 0 {
		opts = append(opts, model.WithTemperature(c.temperature))
	}
	if len(c.tools) > 0 {
		opts = append(opts, model.WithTools(c.tools))
	}
	if c.toolChoice != nil {
		opts = append(opts, model.WithToolChoice(*c.toolChoice))
	}
	return opts
}

// Chat calls Generate and converts the result to an LLMResponse.
func Chat(ctx context.Context, adapter ChatModelAdapter, messages []*schema.Message, opts ...ChatOption) (*types.LLMResponse, error) {
	cfg := &chatConfig{}
	for _, o := range opts {
		o(cfg)
	}

	resp, err := adapter.Generate(ctx, messages, cfg.toModelOptions()...)
	if err != nil {
		return errorResponse(err), nil
	}

	return messageToLLMResponse(resp), nil
}

// ChatStream calls Stream and converts chunks to an LLMResponse, pushing deltas via callbacks.
func ChatStream(ctx context.Context, adapter ChatModelAdapter, messages []*schema.Message, onDelta func(string), onThinking func(string), onToolCall func(map[string]any), opts ...ChatOption) (*types.LLMResponse, error) {
	cfg := &chatConfig{}
	for _, o := range opts {
		o(cfg)
	}

	reader, err := adapter.Stream(ctx, messages, cfg.toModelOptions()...)
	if err != nil {
		return errorResponse(err), nil
	}
	defer reader.Close()

	return collectStream(reader, onDelta, onThinking, onToolCall)
}

// collectStream drains a stream reader and assembles the final LLMResponse.
func collectStream(reader *schema.StreamReader[*schema.Message], onDelta func(string), onThinking func(string), onToolCall func(map[string]any)) (*types.LLMResponse, error) {
	var (
		content      string
		reasoning    string
		toolCalls    []types.ToolCallRequest
		finishReason string
		usage        map[string]int
	)

	for {
		chunk, err := reader.Recv()
		if err != nil {
			break
		}

		if chunk == nil {
			continue
		}

		if chunk.Content != "" {
			content += chunk.Content
			if onDelta != nil {
				onDelta(chunk.Content)
			}
		}

		if chunk.ReasoningContent != "" {
			reasoning += chunk.ReasoningContent
			if onThinking != nil {
				onThinking(chunk.ReasoningContent)
			}
		}

		for _, tc := range chunk.ToolCalls {
			var args map[string]any
			if tc.Function.Arguments != "" {
				json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			if args == nil {
				args = make(map[string]any)
			}
			toolCalls = append(toolCalls, types.ToolCallRequest{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: args,
			})
			if onToolCall != nil {
				onToolCall(args)
			}
		}

		if chunk.ResponseMeta != nil {
			if chunk.ResponseMeta.FinishReason != "" {
				finishReason = chunk.ResponseMeta.FinishReason
			}
			if chunk.ResponseMeta.Usage != nil {
				usage = map[string]int{
					"prompt_tokens":     chunk.ResponseMeta.Usage.PromptTokens,
					"completion_tokens": chunk.ResponseMeta.Usage.CompletionTokens,
					"total_tokens":      chunk.ResponseMeta.Usage.TotalTokens,
				}
			}
		}
	}

	if finishReason == "" {
		finishReason = "stop"
	}

	return &types.LLMResponse{
		Content:          content,
		ToolCalls:        toolCalls,
		FinishReason:     finishReason,
		Usage:            usage,
		ReasoningContent: reasoning,
	}, nil
}

// messageToLLMResponse converts an Eino schema.Message to a nanobot LLMResponse.
func messageToLLMResponse(msg *schema.Message) *types.LLMResponse {
	if msg == nil {
		return &types.LLMResponse{FinishReason: "error"}
	}

	r := &types.LLMResponse{
		Content:          msg.Content,
		ReasoningContent: msg.ReasoningContent,
	}

	if msg.ResponseMeta != nil {
		r.FinishReason = msg.ResponseMeta.FinishReason
		if msg.ResponseMeta.Usage != nil {
			r.Usage = map[string]int{
				"prompt_tokens":     msg.ResponseMeta.Usage.PromptTokens,
				"completion_tokens": msg.ResponseMeta.Usage.CompletionTokens,
				"total_tokens":      msg.ResponseMeta.Usage.TotalTokens,
			}
		}
	}
	if r.FinishReason == "" {
		r.FinishReason = "stop"
	}

	for _, tc := range msg.ToolCalls {
		var args map[string]any
		if tc.Function.Arguments != "" {
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
		}
		if args == nil {
			args = make(map[string]any)
		}
		r.ToolCalls = append(r.ToolCalls, types.ToolCallRequest{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	return r
}

// errorResponse creates an LLMResponse representing a failed call.
func errorResponse(err error) *types.LLMResponse {
	return &types.LLMResponse{
		FinishReason:     "error",
		ErrorKind:        "unknown",
		ErrorShouldRetry: false,
	}
}

// ToolInfosToToolCalls converts Eino ToolCall to OpenAI-compatible tool_call maps.
func ToolCallsToMaps(toolCalls []schema.ToolCall) []map[string]any {
	var out []map[string]any
	for _, tc := range toolCalls {
		var args map[string]any
		if tc.Function.Arguments != "" {
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
		}
		out = append(out, map[string]any{
			"id":   tc.ID,
			"type": "function",
			"function": map[string]any{
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
			},
		})
	}
	return out
}
