package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
	nanotool "github.com/Seagull2ker/nanobot-go/internal/tool"
	"github.com/Seagull2ker/nanobot-go/internal/types"
)

// AgentLoop manages message dispatch with per-session worker goroutines.
// Each session gets a dedicated goroutine that processes messages sequentially.
// Different sessions run in parallel.
type AgentLoop struct {
	bus   *bus.MessageBus
	agent *Agent
	cfg   *AgentConfig

	mu       sync.Mutex
	sessions sync.Map // key(string) -> chan *types.InboundMessage
	wg       sync.WaitGroup
	tasks    map[string]context.CancelFunc
}

// AgentConfig holds configuration for the AgentLoop.
type AgentConfig struct {
	MaxConcurrentSessions int
}

// NewAgentLoop creates a new AgentLoop.
func NewAgentLoop(b *bus.MessageBus, agent *Agent, cfg *AgentConfig) *AgentLoop {
	if cfg == nil {
		cfg = &AgentConfig{MaxConcurrentSessions: 10}
	}
	return &AgentLoop{
		bus:   b,
		agent: agent,
		cfg:   cfg,
		tasks: make(map[string]context.CancelFunc),
	}
}

// Run starts consuming inbound messages and dispatching to per-session workers.
func (l *AgentLoop) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			l.drainWorkers()
			return ctx.Err()
		case msg, ok := <-l.bus.ConsumeInbound():
			if !ok {
				l.drainWorkers()
				return nil
			}
			l.dispatch(ctx, msg)
		}
	}
}

// Wait blocks until all session workers have finished processing.
func (l *AgentLoop) Wait() {
	l.wg.Wait()
}

// dispatch routes a message to its session's worker goroutine.
// Creates a new worker if this is the first message for the session.
func (l *AgentLoop) dispatch(ctx context.Context, msg *types.InboundMessage) {
	key := msg.SessionKey()

	chAny, loaded := l.sessions.LoadOrStore(key, make(chan *types.InboundMessage, 32))
	ch := chAny.(chan *types.InboundMessage)

	if !loaded {
		l.wg.Add(1)
		go l.sessionWorker(ctx, key, ch)
	}

	// Non-blocking send: if the session queue is full, drop.
	select {
	case ch <- msg:
	case <-ctx.Done():
	default:
		slog.Warn("session queue full, dropping message", "session", key)
	}
}

// sessionWorker processes messages for a single session sequentially.
func (l *AgentLoop) sessionWorker(ctx context.Context, key string, ch chan *types.InboundMessage) {
	defer l.wg.Done()
	defer l.sessions.Delete(key)

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			slog.Info("session worker received message", "session", key, "channel", msg.Channel, "content_len", len(msg.Content))
			l.processMessage(ctx, msg)
		}
	}
}

// processMessage handles a single inbound message through the full agent pipeline:
// TurnContext → ChatStream → collect response → publish outbound.
func (l *AgentLoop) processMessage(ctx context.Context, msg *types.InboundMessage) {
	sessionKey := msg.SessionKey()

	// Set up turn-level context.
	taskCtx, turnCtx := nanotool.NewTurnContext(ctx)
	taskCtx = nanotool.ContextWithSessionID(taskCtx, sessionKey)
	taskCtx = nanotool.ContextWithProgressInfo(taskCtx, msg.Channel, msg.ChatID)

	// System channel messages are from subagents — tag as assistant input.
	if msg.Channel == "system" {
		taskCtx = nanotool.ContextWithInputRole(taskCtx, "assistant")
	}

	// Call agent with retry for transient errors.
	content, err := l.chatWithRetry(taskCtx, sessionKey, msg.Content)

	slog.Info("agent turn complete", "session", sessionKey, "content_len", len(content), "error", err)

	// Don't send duplicate response if a tool already sent a message this turn.
	if turnCtx.WasMessageSent() {
		slog.Info("skipping publish — message already sent this turn", "session", sessionKey)
		return
	}

	replyTo := types.ExtractReplyTo(msg.Metadata)

	if err != nil {
		slog.Error("agent chat failed", "session", sessionKey, "error", err)
		content = "Sorry, something went wrong. Please try again."
	}

	if content == "" {
		slog.Info("skipping publish — empty content", "session", sessionKey)
		return
	}

	// Publish response.
	slog.Info("publishing outbound", "session", sessionKey, "channel", msg.Channel, "chat_id", msg.ChatID, "content_len", len(content))
	l.bus.PublishOutbound(context.Background(), &types.OutboundMessage{
		Channel:  msg.Channel,
		ChatID:   msg.ChatID,
		Content:  content,
		ReplyTo:  replyTo,
		Metadata: msg.Metadata,
	})
}

// chatWithRetry calls ChatStream with one retry for transient errors.
func (l *AgentLoop) chatWithRetry(ctx context.Context, sessionKey, input string) (string, error) {
	reader, err := l.agent.ChatStream(ctx, sessionKey, input)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	var content strings.Builder
	for {
		chunk, recvErr := reader.Recv()
		if recvErr != nil {
			break
		}
		if chunk != nil {
			content.WriteString(chunk.Content)
		}
	}
	return content.String(), nil
}

// drainWorkers closes all session channels and waits for workers to finish.
func (l *AgentLoop) drainWorkers() {
	l.sessions.Range(func(key, value any) bool {
		ch := value.(chan *types.InboundMessage)
		close(ch)
		return true
	})
}

// CancelSession cancels an active session task.
func (l *AgentLoop) CancelSession(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cancel, ok := l.tasks[sessionID]; ok {
		cancel()
		delete(l.tasks, sessionID)
	}
}

// CancelAll cancels all active session tasks.
func (l *AgentLoop) CancelAll() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := len(l.tasks)
	for id, cancel := range l.tasks {
		cancel()
		delete(l.tasks, id)
	}
	return count
}
