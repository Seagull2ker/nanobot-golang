package tool

import (
	"context"
	"sync/atomic"
)

// TurnContext tracks per-turn mutable state via context.
// Used to coordinate between tools and the agent loop (e.g.,
// preventing duplicate message sends).

type turnCtxKey struct{}
type sessionIDKey struct{}
type progressInfoKey struct{}
type inputRoleKey struct{}

// TurnContext tracks whether a message has been sent this turn.
type TurnContext struct {
	messageSent atomic.Bool
}

// SetMessageSent marks that a message was sent during this turn.
func (t *TurnContext) SetMessageSent() { t.messageSent.Store(true) }

// WasMessageSent returns true if a message was sent this turn.
func (t *TurnContext) WasMessageSent() bool { return t.messageSent.Load() }

// NewTurnContext creates a fresh TurnContext and attaches it to the parent context.
func NewTurnContext(parent context.Context) (context.Context, *TurnContext) {
	tc := &TurnContext{}
	return context.WithValue(parent, turnCtxKey{}, tc), tc
}

// GetTurnContext retrieves the TurnContext from ctx, or nil.
func GetTurnContext(ctx context.Context) *TurnContext {
	if tc, ok := ctx.Value(turnCtxKey{}).(*TurnContext); ok {
		return tc
	}
	return nil
}

// ProgressInfo carries channel routing info for tool callbacks.
type ProgressInfo struct {
	Channel string
	ChatID  string
}

// ContextWithProgressInfo attaches ProgressInfo to context.
func ContextWithProgressInfo(ctx context.Context, channel, chatID string) context.Context {
	return context.WithValue(ctx, progressInfoKey{}, &ProgressInfo{Channel: channel, ChatID: chatID})
}

// GetProgressInfo retrieves ProgressInfo from ctx.
func GetProgressInfo(ctx context.Context) *ProgressInfo {
	if pi, ok := ctx.Value(progressInfoKey{}).(*ProgressInfo); ok {
		return pi
	}
	return nil
}

// ContextWithSessionID attaches a session ID to context.
func ContextWithSessionID(ctx context.Context, sid string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, sid)
}

// SessionIDFromContext retrieves the session ID from ctx.
func SessionIDFromContext(ctx context.Context) string {
	if sid, ok := ctx.Value(sessionIDKey{}).(string); ok {
		return sid
	}
	return ""
}

// ContextWithInputRole tags the input role ("user", "assistant", "system").
func ContextWithInputRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, inputRoleKey{}, role)
}

// InputRoleFromContext retrieves the input role from ctx.
func InputRoleFromContext(ctx context.Context) string {
	if role, ok := ctx.Value(inputRoleKey{}).(string); ok {
		return role
	}
	return "user"
}
