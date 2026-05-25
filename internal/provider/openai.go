package provider

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Seagull2ker/nanobot-go/internal/config"
	"github.com/Seagull2ker/nanobot-go/internal/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	openai "github.com/sashabaranov/go-openai"
)

// openaiCompatAdapter implements ChatModelAdapter for all OpenAI-compatible APIs.
// Serves OpenAI, DeepSeek, DashScope (and any future openai_compat providers).
type openaiCompatAdapter struct {
	client      *openai.Client
	spec        ProviderSpec
	providerCfg config.ProviderConfig
	tools       []*schema.ToolInfo
}

func newOpenAICompatAdapter(spec ProviderSpec, cfg config.ProviderConfig) (*openaiCompatAdapter, error) {
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = spec.DefaultAPIBase
	}
	apiKey := resolveAPIKey(spec, cfg)

	clientCfg := openai.DefaultConfig(apiKey)
	clientCfg.BaseURL = apiBase

	if len(cfg.ExtraHeaders) > 0 {
		clientCfg.HTTPClient = nil // let the SDK handle it; extra headers go in request
	}

	return &openaiCompatAdapter{
		client:      openai.NewClientWithConfig(clientCfg),
		spec:        spec,
		providerCfg: cfg,
	}, nil
}

func (a *openaiCompatAdapter) BindTools(tools []*schema.ToolInfo) error {
	a.tools = tools
	return nil
}

func (a *openaiCompatAdapter) GetDefaultModel() string {
	return a.spec.DefaultModel
}

func (a *openaiCompatAdapter) SupportsThinking() bool {
	return a.spec.SupportsThinking
}

// Generate implements model.BaseChatModel.
func (a *openaiCompatAdapter) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	req, err := a.buildRequest(input, opts...)
	if err != nil {
		return nil, err
	}
	req.Stream = false

	resp, err := a.client.CreateChatCompletion(ctx, *req)
	if err != nil {
		return nil, classifyError(err)
	}

	return a.responseToMessage(&resp), nil
}

// Stream implements model.BaseChatModel.
func (a *openaiCompatAdapter) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	req, err := a.buildRequest(input, opts...)
	if err != nil {
		return nil, err
	}
	req.Stream = true
	req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}

	stream, err := a.client.CreateChatCompletionStream(ctx, *req)
	if err != nil {
		return nil, classifyError(err)
	}

	sr, sw := schema.Pipe[*schema.Message](10)
	go a.pumpStream(stream, sw)
	return sr, nil
}

func (a *openaiCompatAdapter) pumpStream(stream *openai.ChatCompletionStream, sw *schema.StreamWriter[*schema.Message]) {
	defer sw.Close()
	defer stream.Close()

	for {
		resp, recvErr := stream.Recv()
		if recvErr != nil {
			return
		}

		msg := a.streamChunkToMessage(&resp)
		if msg != nil {
			if sw.Send(msg, nil) {
				return
			}
		}
	}
}

func (a *openaiCompatAdapter) buildRequest(input []*schema.Message, opts ...model.Option) (*openai.ChatCompletionRequest, error) {
	common := model.GetCommonOptions(nil, opts...)

	modelName := a.spec.DefaultModel
	if common.Model != nil && *common.Model != "" {
		modelName = *common.Model
	}
	// Strip provider prefix if needed (e.g., "openai/gpt-4o" → "gpt-4o").
	if a.spec.StripModelPrefix && strings.Contains(modelName, "/") {
		modelName = modelName[strings.LastIndex(modelName, "/")+1:]
	}

	messages := a.messagesToOpenAI(input)

	req := &openai.ChatCompletionRequest{
		Model:    modelName,
		Messages: messages,
	}

	if common.MaxTokens != nil {
		req.MaxTokens = *common.MaxTokens
	}
	if common.Temperature != nil {
		req.Temperature = *common.Temperature
	}
	if common.TopP != nil {
		req.TopP = *common.TopP
	}
	if len(common.Stop) > 0 {
		req.Stop = common.Stop
	}
	tools := common.Tools
	if len(tools) == 0 {
		tools = a.tools
	}
	if len(tools) > 0 {
		req.Tools = toolsToOpenAI(tools)
	}
	if common.ToolChoice != nil {
		switch *common.ToolChoice {
		case schema.ToolChoiceForbidden:
			req.ToolChoice = "none"
		case schema.ToolChoiceForced:
			req.ToolChoice = "required"
		case schema.ToolChoiceAllowed:
			req.ToolChoice = "auto"
		}
	}

	a.applyThinking(req, modelName, "")
	a.applyExtraBody(req)

	return req, nil
}

func (a *openaiCompatAdapter) applyThinking(req *openai.ChatCompletionRequest, model string, reasoningEffort string) {
	if a.spec.ModelOverrides != nil {
		if override, ok := a.spec.ModelOverrides[model]; ok {
			if override.ReasoningEffort != nil {
				reasoningEffort = *override.ReasoningEffort
			}
		}
	}

	switch a.spec.ThinkingStyle {
	case ThinkingType:
		if req.ChatTemplateKwargs == nil {
			req.ChatTemplateKwargs = make(map[string]any)
		}
		req.ChatTemplateKwargs["thinking"] = map[string]string{"type": "enabled"}
	case ThinkingEnabled:
		if req.ChatTemplateKwargs == nil {
			req.ChatTemplateKwargs = make(map[string]any)
		}
		req.ChatTemplateKwargs["enable_thinking"] = true
	case ThinkingReasoningSplit:
		if reasoningEffort != "" {
			req.ReasoningEffort = reasoningEffort
		}
	}
}

func (a *openaiCompatAdapter) applyExtraBody(req *openai.ChatCompletionRequest) {
	if len(a.providerCfg.ExtraBody) == 0 {
		return
	}
	// Merge extra_body via ChatTemplateKwargs (go-openai's mechanism for non-standard params).
	if req.ChatTemplateKwargs == nil {
		req.ChatTemplateKwargs = make(map[string]any)
	}
	for k, v := range a.providerCfg.ExtraBody {
		req.ChatTemplateKwargs[k] = v
	}
}

// messagesToOpenAI converts Eino schema.Messages to OpenAI format.
func (a *openaiCompatAdapter) messagesToOpenAI(messages []*schema.Message) []openai.ChatCompletionMessage {
	var out []openai.ChatCompletionMessage
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		m := openai.ChatCompletionMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
			Name:    msg.Name,
		}

		// Handle tool calls (assistant messages).
		for _, tc := range msg.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, openai.ToolCall{
				ID:   tc.ID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}

		// Handle tool result (tool messages).
		if msg.ToolCallID != "" {
			m.ToolCallID = msg.ToolCallID
			m.Role = "tool"
		}
		if msg.ToolName != "" {
			m.Name = msg.ToolName
		}

		out = append(out, m)
	}
	return out
}

// responseToMessage converts an OpenAI ChatCompletionResponse to an Eino schema.Message.
func (a *openaiCompatAdapter) responseToMessage(resp *openai.ChatCompletionResponse) *schema.Message {
	if len(resp.Choices) == 0 {
		return &schema.Message{
			Role:    schema.Assistant,
			Content: "",
			ResponseMeta: &schema.ResponseMeta{
				FinishReason: "error",
			},
		}
	}

	choice := resp.Choices[0]
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: choice.Message.Content,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: string(choice.FinishReason),
			Usage: &schema.TokenUsage{
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				TotalTokens:      resp.Usage.TotalTokens,
			},
		},
		ReasoningContent: choice.Message.ReasoningContent,
	}

	for _, tc := range choice.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID:   tc.ID,
			Type: string(tc.Type),
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	return msg
}

// streamChunkToMessage converts a streaming delta to a partial message.
func (a *openaiCompatAdapter) streamChunkToMessage(resp *openai.ChatCompletionStreamResponse) *schema.Message {
	if len(resp.Choices) == 0 {
		if resp.Usage != nil {
			return &schema.Message{
				Role: schema.Assistant,
				ResponseMeta: &schema.ResponseMeta{
					Usage: &schema.TokenUsage{
						PromptTokens:     resp.Usage.PromptTokens,
						CompletionTokens: resp.Usage.CompletionTokens,
						TotalTokens:      resp.Usage.TotalTokens,
					},
				},
			}
		}
		return nil
	}

	delta := resp.Choices[0].Delta
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: delta.Content,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: string(resp.Choices[0].FinishReason),
		},
		ReasoningContent: delta.ReasoningContent,
	}

	for _, tc := range delta.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID:   tc.ID,
			Type: string(tc.Type),
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	if resp.Usage != nil {
		msg.ResponseMeta.Usage = &schema.TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
	}

	return msg
}

// toolsToOpenAI converts Eino ToolInfo to OpenAI Tool.
func toolsToOpenAI(tools []*schema.ToolInfo) []openai.Tool {
	var out []openai.Tool
	for _, t := range tools {
		tool := openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Desc,
			},
		}
		if t.ParamsOneOf != nil {
			js, err := t.ParamsOneOf.ToJSONSchema()
			if err == nil && js != nil {
				params := jsToMap(js)
				if params != nil {
					tool.Function.Parameters = params
				}
			}
		}
		out = append(out, tool)
	}
	return out
}

func jsToMap(js interface{}) map[string]any {
	data, err := json.Marshal(js)
	if err != nil {
		return nil
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	return m
}

// resolveAPIKey resolves the API key priority: env(spec.EnvKey) > config file > env(EnvKey).
func resolveAPIKey(spec ProviderSpec, cfg config.ProviderConfig) string {
	// Placeholder — actual env lookup will be done when integrating with config.
	// For now, use the config value directly.
	return cfg.APIKey
}

// classifyError converts an OpenAI API error to a structured error.
func classifyError(err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

	// Check for well-known error conditions.
	if strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized") {
		return errors.Wrap(errors.KindUnauthorized, "openai_compat", err)
	}
	if strings.Contains(lower, "403") || strings.Contains(lower, "forbidden") {
		return errors.Wrap(errors.KindUnauthorized, "openai_compat", err)
	}
	if strings.Contains(lower, "404") || strings.Contains(lower, "not found") {
		return errors.Wrap(errors.KindNotFound, "openai_compat", err)
	}
	if strings.Contains(lower, "429") {
		if strings.Contains(lower, "quota") || strings.Contains(lower, "insufficient") {
			return errors.Wrap(errors.KindInvalid, "openai_compat", err)
		}
		return errors.Wrap(errors.KindRateLimited, "openai_compat", err)
	}
	if strings.Contains(lower, "503") || strings.Contains(lower, "502") || strings.Contains(lower, "504") {
		return errors.Wrap(errors.KindUnavailable, "openai_compat", err)
	}
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline") {
		return errors.Wrap(errors.KindTimeout, "openai_compat", err)
	}
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") {
		return errors.Wrap(errors.KindNetwork, "openai_compat", err)
	}

	return errors.Wrap(errors.KindUnknown, "openai_compat", err)
}
