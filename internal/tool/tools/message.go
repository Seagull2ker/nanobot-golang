package tools

import (
	"context"
	"fmt"

	"github.com/Seagull2ker/nanobot-go/internal/tool"
	"github.com/Seagull2ker/nanobot-go/internal/types"
)

// SendMessageFunc is the callback for delivering messages via channels.
type SendMessageFunc func(ctx context.Context, msg *types.OutboundMessage) error

var sendMessageFn SendMessageFunc

// SetSendMessageFunc sets the callback used by the message tool.
func SetSendMessageFunc(fn SendMessageFunc) {
	sendMessageFn = fn
}

type messageTool struct{}

func init() { tool.Register(&messageTool{}) }

func (t *messageTool) Name() string          { return "message" }
func (t *messageTool) ReadOnly() bool        { return false }
func (t *messageTool) ConcurrencySafe() bool { return true }
func (t *messageTool) Exclusive() bool       { return false }

func (t *messageTool) Description() string {
	return "Send a message to the user. Use this to notify the user about important results or ask clarifying questions."
}

func (t *messageTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The message content to send to the user",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Channel to send to (defaults to current channel)",
			},
			"chat_id": map[string]any{
				"type":        "string",
				"description": "Chat ID to send to (defaults to current chat)",
			},
		},
		"required": []string{"content"},
	}
}

func (t *messageTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	content, _ := params["content"].(string)
	if content == "" {
		return &tool.Result{Content: "Error: content is required"}, nil
	}

	if sendMessageFn == nil {
		return &tool.Result{Content: "Message delivery not configured"}, nil
	}

	channel, _ := params["channel"].(string)
	chatID, _ := params["chat_id"].(string)

	err := sendMessageFn(ctx, &types.OutboundMessage{
		Channel: channel,
		ChatID:  chatID,
		Content: content,
	})
	if err != nil {
		return &tool.Result{Content: fmt.Sprintf("Error sending message: %v", err)}, nil
	}

	return &tool.Result{Content: "Message sent successfully"}, nil
}
