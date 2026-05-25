package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/Seagull2ker/nanobot-go/internal/config"
	"github.com/Seagull2ker/nanobot-go/internal/errors"
)

const anthropicAPIVersion = "2023-06-01"

type anthropicAdapter struct {
	apiKey      string
	apiBase     string
	httpClient  *http.Client
	spec        ProviderSpec
	providerCfg config.ProviderConfig
	tools       []*schema.ToolInfo
}

func newAnthropicAdapter(spec ProviderSpec, cfg config.ProviderConfig) (*anthropicAdapter, error) {
	apiBase := cfg.APIBase
	if apiBase == "" {
		apiBase = spec.DefaultAPIBase
	}
	apiKey := resolveAPIKey(spec, cfg)

	return &anthropicAdapter{
		apiKey:      apiKey,
		apiBase:     strings.TrimSuffix(apiBase, "/"),
		httpClient:  &http.Client{},
		spec:        spec,
		providerCfg: cfg,
	}, nil
}

func (a *anthropicAdapter) BindTools(tools []*schema.ToolInfo) error {
	a.tools = tools
	return nil
}

func (a *anthropicAdapter) GetDefaultModel() string {
	return a.spec.DefaultModel
}

func (a *anthropicAdapter) SupportsThinking() bool {
	return a.spec.SupportsThinking
}

// Generate implements model.BaseChatModel.
func (a *anthropicAdapter) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	body, err := a.buildRequestBody(input, opts...)
	if err != nil {
		return nil, err
	}
	body["stream"] = false

	resp, err := a.apiCall(ctx, body)
	if err != nil {
		return nil, err
	}

	return a.responseToMessage(resp), nil
}

// Stream implements model.BaseChatModel.
func (a *anthropicAdapter) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	body, err := a.buildRequestBody(input, opts...)
	if err != nil {
		return nil, err
	}
	body["stream"] = true

	sr, sw := schema.Pipe[*schema.Message](10)
	go a.streamCall(ctx, body, sw)
	return sr, nil
}

func (a *anthropicAdapter) buildRequestBody(input []*schema.Message, opts ...model.Option) (map[string]any, error) {
	common := model.GetCommonOptions(nil, opts...)

	modelName := a.spec.DefaultModel
	if common.Model != nil && *common.Model != "" {
		modelName = *common.Model
	}
	if a.spec.StripModelPrefix && strings.Contains(modelName, "/") {
		modelName = modelName[strings.LastIndex(modelName, "/")+1:]
	}

	maxTokens := 4096
	if common.MaxTokens != nil {
		maxTokens = *common.MaxTokens
	}

	body := map[string]any{
		"model":      modelName,
		"max_tokens": maxTokens,
	}

	// Extract system messages to the top-level system field.
	systemMessages, chatMessages := splitSystemMessages(input)
	if len(systemMessages) > 0 {
		var sysContent []map[string]any
		for _, m := range systemMessages {
			sysContent = append(sysContent, map[string]any{
				"type": "text",
				"text": m.Content,
			})
		}
		if len(sysContent) == 1 {
			body["system"] = sysContent[0]["text"]
		} else {
			body["system"] = sysContent
		}
	}

	// Convert messages to Anthropic format.
	var anthropicMsgs []map[string]any
	for _, m := range chatMessages {
		converted := a.messageToAnthropic(m)
		if converted != nil {
			anthropicMsgs = append(anthropicMsgs, converted)
		}
	}
	body["messages"] = anthropicMsgs

	tools := common.Tools
	if len(tools) == 0 {
		tools = a.tools
	}
	if len(tools) > 0 {
		body["tools"] = toolsToAnthropic(tools)
	}
	if common.ToolChoice != nil {
		switch *common.ToolChoice {
		case schema.ToolChoiceForbidden:
			body["tool_choice"] = map[string]string{"type": "none"}
		case schema.ToolChoiceForced:
			body["tool_choice"] = map[string]string{"type": "any"}
		case schema.ToolChoiceAllowed:
			body["tool_choice"] = map[string]string{"type": "auto"}
		}
	}

	// Extended thinking.
	if a.spec.SupportsThinking {
		thinkingBudget := 1024
		if maxTokens > 8192 {
			thinkingBudget = 8192
		}
		body["thinking"] = map[string]any{
			"type":          "enabled",
			"budget_tokens": thinkingBudget,
		}
	}

	// Temperature.
	if common.Temperature != nil {
		body["temperature"] = *common.Temperature
	}

	return body, nil
}

// splitSystemMessages partitions messages into system and non-system.
func splitSystemMessages(messages []*schema.Message) (system, rest []*schema.Message) {
	for _, m := range messages {
		if m.Role == schema.System {
			system = append(system, m)
		} else {
			rest = append(rest, m)
		}
	}
	return
}

func (a *anthropicAdapter) messageToAnthropic(msg *schema.Message) map[string]any {
	m := map[string]any{
		"role": string(msg.Role),
	}

	if msg.Role == schema.Tool {
		return map[string]any{
			"role": "user",
			"content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     msg.Content,
			}},
		}
	}

	if msg.Role == schema.Assistant && len(msg.ToolCalls) > 0 {
		var content []map[string]any
		if msg.Content != "" {
			content = append(content, map[string]any{
				"type": "text",
				"text": msg.Content,
			})
		}
		for _, tc := range msg.ToolCalls {
			var input map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &input)
			if input == nil {
				input = make(map[string]any)
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Function.Name,
				"input": input,
			})
		}
		m["content"] = content
	} else {
		m["content"] = msg.Content
	}

	return m
}

func toolsToAnthropic(tools []*schema.ToolInfo) []map[string]any {
	var out []map[string]any
	for _, t := range tools {
		tool := map[string]any{
			"name":        t.Name,
			"description": t.Desc,
		}
		if t.ParamsOneOf != nil {
			js, err := t.ParamsOneOf.ToJSONSchema()
			if err == nil && js != nil {
				tool["input_schema"] = jsToMap(js)
			}
		} else {
			tool["input_schema"] = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		out = append(out, tool)
	}
	return out
}

// apiCall sends a non-streaming request to the Anthropic Messages API.
func (a *anthropicAdapter) apiCall(ctx context.Context, body map[string]any) (map[string]any, error) {
	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", a.apiBase+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, errors.Wrap(errors.KindNetwork, "anthropic", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, classifyHTTPError(err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var result map[string]any
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, errors.Wrap(errors.KindUnknown, "anthropic", err)
	}

	if resp.StatusCode >= 400 {
		return nil, classifyAnthropicError(resp.StatusCode, result)
	}

	return result, nil
}

// streamCall handles SSE streaming from the Anthropic Messages API.
func (a *anthropicAdapter) streamCall(ctx context.Context, body map[string]any, sw *schema.StreamWriter[*schema.Message]) {
	defer sw.Close()

	data, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", a.apiBase+"/v1/messages", bytes.NewReader(data))
	if err != nil {
		sw.Send(nil, errors.Wrap(errors.KindNetwork, "anthropic", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		sw.Send(nil, classifyHTTPError(err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		var errBody map[string]any
		json.Unmarshal(bodyBytes, &errBody)
		sw.Send(nil, classifyAnthropicError(resp.StatusCode, errBody))
		return
	}

	a.parseSSE(resp.Body, sw)
}

func (a *anthropicAdapter) parseSSE(body io.Reader, sw *schema.StreamWriter[*schema.Message]) {
	scanner := bufio.NewScanner(body)
	var (
		content   string
		reasoning string
		toolCalls []schema.ToolCall
		toolIndex int
		usage     *schema.TokenUsage
	)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]

		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "message_start":
			if u, ok := event["message"].(map[string]any); ok {
				if usageMap, ok := u["usage"].(map[string]any); ok {
					usage = &schema.TokenUsage{
						PromptTokens:     int(usageMap["input_tokens"].(float64)),
						CompletionTokens: int(usageMap["output_tokens"].(float64)),
						TotalTokens:      int(usageMap["input_tokens"].(float64)) + int(usageMap["output_tokens"].(float64)),
					}
				}
			}

		case "content_block_start":
			if contentBlock, ok := event["content_block"].(map[string]any); ok {
				cbType, _ := contentBlock["type"].(string)
				if cbType == "tool_use" {
					toolCalls = append(toolCalls, schema.ToolCall{
						ID:   contentBlock["id"].(string),
						Type: "function",
						Function: schema.FunctionCall{
							Name:      contentBlock["name"].(string),
							Arguments: "",
						},
					})
					toolIndex = len(toolCalls) - 1
				}
			}

		case "content_block_delta":
			delta, _ := event["delta"].(map[string]any)
			deltaType, _ := delta["type"].(string)
			switch deltaType {
			case "text_delta":
				text, _ := delta["text"].(string)
				content += text
				sw.Send(&schema.Message{
					Role:    schema.Assistant,
					Content: text,
				}, nil)
			case "thinking_delta":
				thinking, _ := delta["thinking"].(string)
				reasoning += thinking
				sw.Send(&schema.Message{
					Role:             schema.Assistant,
					ReasoningContent: thinking,
				}, nil)
			case "input_json_delta":
				partial, _ := delta["partial_json"].(string)
				if toolIndex < len(toolCalls) {
					toolCalls[toolIndex].Function.Arguments += partial
				}
			}

		case "message_delta":
			finishReason := "stop"
			if d, ok := event["delta"].(map[string]any); ok {
				if fr, ok := d["stop_reason"].(string); ok {
					finishReason = anthropicFinishReason(fr)
				}
			}
			if usageMap, ok := event["usage"].(map[string]any); ok {
				if usage == nil {
					usage = &schema.TokenUsage{}
				}
				if out, ok := usageMap["output_tokens"].(float64); ok {
					usage.CompletionTokens = int(out)
					usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
				}
			}
			// Send final message with metadata.
			finalMsg := &schema.Message{
				Role:    schema.Assistant,
				Content: content,
				ResponseMeta: &schema.ResponseMeta{
					FinishReason: finishReason,
					Usage:        usage,
				},
				ReasoningContent: reasoning,
				ToolCalls:        toolCalls,
			}
			sw.Send(finalMsg, nil)

		case "error":
			errMsg, _ := event["error"].(map[string]any)
			msg, _ := errMsg["message"].(string)
			sw.Send(nil, fmt.Errorf("anthropic: %s", msg))
		}
	}
}

func anthropicFinishReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence":
		return "stop"
	default:
		return reason
	}
}

// responseToMessage converts an Anthropic API response to an Eino schema.Message.
func (a *anthropicAdapter) responseToMessage(resp map[string]any) *schema.Message {
	msg := &schema.Message{
		Role: schema.Assistant,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: "stop",
		},
	}

	// Stop reason.
	if sr, ok := resp["stop_reason"].(string); ok {
		msg.ResponseMeta.FinishReason = anthropicFinishReason(sr)
	}

	// Usage.
	if usageMap, ok := resp["usage"].(map[string]any); ok {
		msg.ResponseMeta.Usage = &schema.TokenUsage{
			PromptTokens:     int(usageMap["input_tokens"].(float64)),
			CompletionTokens: int(usageMap["output_tokens"].(float64)),
		}
		msg.ResponseMeta.Usage.TotalTokens = msg.ResponseMeta.Usage.PromptTokens + msg.ResponseMeta.Usage.CompletionTokens
	}

	// Content blocks.
	contentBlocks, _ := resp["content"].([]any)
	for _, block := range contentBlocks {
		cb, _ := block.(map[string]any)
		cbType, _ := cb["type"].(string)

		switch cbType {
		case "text":
			msg.Content += cb["text"].(string)
		case "tool_use":
			args, _ := json.Marshal(cb["input"])
			msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
				ID:   cb["id"].(string),
				Type: "function",
				Function: schema.FunctionCall{
					Name:      cb["name"].(string),
					Arguments: string(args),
				},
			})
		case "thinking":
			if thinking, ok := cb["thinking"].(string); ok {
				msg.ReasoningContent += thinking
			}
		}
	}

	if msg.ResponseMeta.FinishReason == "end_turn" {
		msg.ResponseMeta.FinishReason = "stop"
	}

	return msg
}

func classifyAnthropicError(statusCode int, body map[string]any) error {
	errType := ""
	if t, ok := body["type"].(string); ok {
		errType = t
	}
	errMsg := ""
	if m, ok := body["error"].(map[string]any); ok {
		if msg, ok := m["message"].(string); ok {
			errMsg = msg
		}
	} else if msg, ok := body["message"].(string); ok {
		errMsg = msg
	}

	switch {
	case statusCode == 401 || statusCode == 403:
		return errors.Wrap(errors.KindUnauthorized, "anthropic", fmt.Errorf("%s: %s", errType, errMsg))
	case statusCode == 429:
		return errors.Wrap(errors.KindRateLimited, "anthropic", fmt.Errorf("%s: %s", errType, errMsg))
	case statusCode >= 500:
		return errors.Wrap(errors.KindUnavailable, "anthropic", fmt.Errorf("%s: %s", errType, errMsg))
	default:
		return errors.Wrap(errors.KindUnknown, "anthropic", fmt.Errorf("%s: %s", errType, errMsg))
	}
}

func classifyHTTPError(err error) error {
	msg := err.Error()
	lower := strings.ToLower(msg)

	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline") {
		return errors.Wrap(errors.KindTimeout, "anthropic", err)
	}
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") {
		return errors.Wrap(errors.KindNetwork, "anthropic", err)
	}

	return errors.Wrap(errors.KindNetwork, "anthropic", err)
}
