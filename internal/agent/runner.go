package agent

import (
	"context"

	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// AgentRunner wraps a react.Agent with recovery logic for two failure modes:
//
//  1. Empty response recovery: when the agent returns neither content nor
//     tool calls, re-prompt with "Please provide your response." (up to
//     MaxEmptyRetries times).
//
//  2. Length truncation recovery: when finish_reason="length", the model
//     was cut off mid-response. Append a continuation prompt (up to
//     MaxLengthRecoveries times).
//
// This is the AgentRunner layer from plan.md — it sits above Eino's ReAct
// loop and below the AgentLoop message dispatch.
type AgentRunner struct {
	agent  *react.Agent
	hooks  *CompositeHook
	config RunnerConfig
}

// RunnerConfig configures recovery behavior.
type RunnerConfig struct {
	MaxEmptyRetries     int // default 2
	MaxLengthRecoveries int // default 3
}

// DefaultRunnerConfig returns default runner configuration.
func DefaultRunnerConfig() RunnerConfig {
	return RunnerConfig{
		MaxEmptyRetries:     2,
		MaxLengthRecoveries: 3,
	}
}

// NewAgentRunner creates an AgentRunner.
func NewAgentRunner(agent *react.Agent, hooks *CompositeHook, cfg RunnerConfig) *AgentRunner {
	return &AgentRunner{
		agent:  agent,
		hooks:  hooks,
		config: cfg,
	}
}

// ChatResult contains the result of an agent chat turn.
type ChatResult struct {
	Content   string
	ToolCalls []ToolCallInfo
	Usage     map[string]int
	Messages  []*schema.Message
}

// Run executes a single agent turn. It loops internally:
// runOnce → check empty → re-prompt → check length → continue prompt → return.
// Stops when a valid result is obtained or recovery budgets are exhausted.
func (r *AgentRunner) Run(ctx context.Context, messages []*schema.Message) (*ChatResult, error) {
	emptyRetries := 0
	lengthRecoveries := 0

	for {
		result, err := r.runOnce(ctx, messages)
		if err != nil {
			return nil, err
		}

		// Empty response recovery
		if result.Content == "" && len(result.ToolCalls) == 0 && emptyRetries < r.config.MaxEmptyRetries {
			emptyRetries++
			messages = append(messages, schema.UserMessage("Please provide your response."))
			continue
		}

		// Length truncation recovery
		if result.FinishReason() == "length" && lengthRecoveries < r.config.MaxLengthRecoveries {
			lengthRecoveries++
			messages = append(messages, schema.UserMessage("Please continue from where you were cut off."))
			continue
		}

		return result, nil
	}
}

func (r *AgentRunner) runOnce(ctx context.Context, messages []*schema.Message) (*ChatResult, error) {
	reader, err := r.agent.Stream(ctx, messages)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	result := &ChatResult{}

	for {
		chunk, err := reader.Recv()
		if err != nil {
			break
		}
		if chunk == nil {
			continue
		}

		result.Content += chunk.Content

		for _, tc := range chunk.ToolCalls {
			result.ToolCalls = append(result.ToolCalls, ToolCallInfo{
				ID:   tc.ID,
				Name: tc.Function.Name,
			})
		}

		if chunk.ResponseMeta != nil {
			if chunk.ResponseMeta.Usage != nil {
				result.Usage = map[string]int{
					"prompt_tokens":     chunk.ResponseMeta.Usage.PromptTokens,
					"completion_tokens": chunk.ResponseMeta.Usage.CompletionTokens,
					"total_tokens":      chunk.ResponseMeta.Usage.TotalTokens,
				}
			}
		}
	}

	result.Messages = []*schema.Message{
		schema.AssistantMessage(result.Content, nil),
	}

	return result, nil
}

// FinishReason returns the finish reason of the last collected message.
func (r *ChatResult) FinishReason() string {
	if len(r.Messages) > 0 && r.Messages[len(r.Messages)-1].ResponseMeta != nil {
		return r.Messages[len(r.Messages)-1].ResponseMeta.FinishReason
	}
	if len(r.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "stop"
}
