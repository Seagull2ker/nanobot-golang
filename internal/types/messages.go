package types

import (
	"strings"
	"time"
)

// InboundMessage is received from a channel for agent processing.
type InboundMessage struct {
	Channel            string
	SenderID           string
	ChatID             string
	Content            string
	Timestamp          time.Time
	Media              []string
	Metadata           map[string]any
	SessionKeyOverride string
}

// SessionKey returns the session identifier.
func (m *InboundMessage) SessionKey() string {
	if m.SessionKeyOverride != "" {
		return m.SessionKeyOverride
	}
	return m.Channel + ":" + m.ChatID
}

// ExtractReplyTo returns message_id from metadata when available.
func ExtractReplyTo(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	if id, ok := metadata["message_id"].(string); ok {
		return strings.TrimSpace(id)
	}
	return ""
}

// OutboundMessage is produced by the agent for delivery through a channel.
type OutboundMessage struct {
	Channel  string
	ChatID   string
	Content  string
	ReplyTo  string
	Media    []string
	Metadata map[string]any
}
