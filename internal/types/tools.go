package types

// ToolCallRequest is a tool call from the LLM.
type ToolCallRequest struct {
	ID                             string
	Name                           string
	Arguments                      map[string]any
	ExtraContent                   map[string]any
	ProviderSpecificFields         map[string]any
	FunctionProviderSpecificFields map[string]any
}

// ToOpenAIToolCall serializes to an OpenAI-style tool_call payload.
func (t *ToolCallRequest) ToOpenAIToolCall() map[string]any {
	tc := map[string]any{
		"id":   t.ID,
		"type": "function",
		"function": map[string]any{
			"name":      t.Name,
			"arguments": t.Arguments,
		},
	}
	if t.ExtraContent != nil {
		tc["extra_content"] = t.ExtraContent
	}
	if t.ProviderSpecificFields != nil {
		tc["provider_specific_fields"] = t.ProviderSpecificFields
	}
	if t.FunctionProviderSpecificFields != nil {
		tc["function"].(map[string]any)["provider_specific_fields"] = t.FunctionProviderSpecificFields
	}
	return tc
}

// LLMResponse is a response from an LLM provider.
type LLMResponse struct {
	Content      string
	ToolCalls    []ToolCallRequest
	FinishReason string
	Usage        map[string]int
	RetryAfter   float64

	// Extended thinking / reasoning
	ReasoningContent string
	ThinkingBlocks   []map[string]any

	// Structured error metadata
	ErrorStatusCode  int
	ErrorKind        string
	ErrorType        string
	ErrorCode        string
	ErrorRetryAfterS float64
	ErrorShouldRetry bool
}

// HasToolCalls checks if the response contains tool calls.
func (r *LLMResponse) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// ShouldExecuteTools checks if tools should be executed.
func (r *LLMResponse) ShouldExecuteTools() bool {
	if !r.HasToolCalls() {
		return false
	}
	return r.FinishReason == "tool_calls" || r.FinishReason == "function_call" || r.FinishReason == "stop"
}
