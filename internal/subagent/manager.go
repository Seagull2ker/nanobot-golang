package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/Seagull2ker/nanobot-go/internal/bus"
	"github.com/Seagull2ker/nanobot-go/internal/provider"
	"github.com/Seagull2ker/nanobot-go/internal/tool"
	"github.com/Seagull2ker/nanobot-go/internal/types"
)

// toolAdapter wraps a nanobot Tool as an Eino InvokableTool.
type toolAdapter struct {
	t tool.Tool
}

func (a *toolAdapter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: a.t.Name(), Desc: a.t.Description()}, nil
}

func (a *toolAdapter) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
	var params map[string]any
	_ = json.Unmarshal([]byte(argumentsInJSON), &params)
	if params == nil {
		params = make(map[string]any)
	}
	result, err := a.t.Execute(ctx, params)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	return result.Content, nil
}

// ---------- Manager ----------

// Manager spawns and manages background subagent tasks.
// Subagents get a restricted tool set (no message, spawn, cron, MCP)
// and report results through the message bus.
type SubagentManager struct {
	chatModel provider.ChatModelAdapter
	allTools  []tool.Tool
	bus       *bus.MessageBus
	maxStep   int

	taskCounter  atomic.Int64
	runningTasks sync.Map
	sessionTasks sync.Map
}

// NewSubagentManager creates a SubagentManager.
func NewSubagentManager(chatModel provider.ChatModelAdapter, allTools []tool.Tool, b *bus.MessageBus, maxStep int) *SubagentManager {
	if maxStep <= 0 {
		maxStep = 15
	}
	return &SubagentManager{
		chatModel: chatModel,
		allTools:  allTools,
		bus:       b,
		maxStep:   maxStep,
	}
}

// Spawn launches a background subagent task. Returns the task ID.
func (m *SubagentManager) Spawn(ctx context.Context, task, label, channel, chatID, sessionKey string) (string, error) {
	taskID := fmt.Sprintf("sub_%d", m.taskCounter.Add(1))

	subAgent, err := m.buildSubAgent(ctx)
	if err != nil {
		return "", fmt.Errorf("spawn: build agent: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	m.runningTasks.Store(taskID, cancel)
	m.addToSession(sessionKey, taskID)

	go m.runSubAgent(ctx, taskID, task, label, channel, chatID, sessionKey, subAgent, cancel)
	return taskID, nil
}

// CancelBySession cancels all subagent tasks for a session.
func (m *SubagentManager) CancelBySession(sessionKey string) int {
	val, ok := m.sessionTasks.Load(sessionKey)
	if !ok {
		return 0
	}
	taskIDs := val.(*sync.Map)
	count := 0
	taskIDs.Range(func(key, _ any) bool {
		id := key.(string)
		if cancel, ok := m.runningTasks.Load(id); ok {
			cancel.(context.CancelFunc)()
			m.runningTasks.Delete(id)
			count++
		}
		return true
	})
	m.sessionTasks.Delete(sessionKey)
	return count
}

// CancelAll cancels all running subagent tasks.
func (m *SubagentManager) CancelAll() int {
	count := 0
	m.runningTasks.Range(func(key, value any) bool {
		value.(context.CancelFunc)()
		m.runningTasks.Delete(key)
		count++
		return true
	})
	return count
}

// RunningCount returns the number of active subagent tasks.
func (m *SubagentManager) RunningCount() int {
	count := 0
	m.runningTasks.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func (m *SubagentManager) addToSession(sessionKey, taskID string) {
	val, _ := m.sessionTasks.LoadOrStore(sessionKey, &sync.Map{})
	taskIDs := val.(*sync.Map)
	taskIDs.Store(taskID, struct{}{})
}

func (m *SubagentManager) buildSubAgent(ctx context.Context) (*react.Agent, error) {
	restrictedTools := m.filterTools()
	einoBaseTools := make([]einotool.BaseTool, len(restrictedTools))
	for i, t := range restrictedTools {
		einoBaseTools[i] = &toolAdapter{t: t}
	}

	return react.NewAgent(ctx, &react.AgentConfig{
		Model: m.chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: einoBaseTools,
		},
		MaxStep: m.maxStep,
	})
}

func (m *SubagentManager) filterTools() []tool.Tool {
	allowed := map[string]bool{
		"read_file": true, "write_file": true, "edit_file": true, "list_dir": true,
		"exec": true, "web_fetch": true, "web_search": true,
	}
	var result []tool.Tool
	for _, t := range m.allTools {
		if allowed[t.Name()] {
			result = append(result, t)
		}
	}
	return result
}

func (m *SubagentManager) runSubAgent(ctx context.Context, taskID, task, label, channel, chatID, sessionKey string, agent *react.Agent, cancel context.CancelFunc) {
	defer cancel()
	defer m.runningTasks.Delete(taskID)

	startTime := time.Now()

	messages := []*schema.Message{
		schema.SystemMessage(fmt.Sprintf(
			"You are a focused sub-agent. Complete this task and report the result concisely.\nTask: %s\nLabel: %s",
			task, label,
		)),
	}

	reader, err := agent.Stream(ctx, messages)
	if err != nil {
		m.notifyCompletion(sessionKey, taskID, label, channel, chatID, fmt.Sprintf("Subagent error: %v", err))
		return
	}
	defer reader.Close()

	var content string
	for {
		chunk, recvErr := reader.Recv()
		if recvErr != nil {
			break
		}
		if chunk != nil {
			content += chunk.Content
		}
	}

	if content == "" {
		content = "Subagent completed with no output."
	}

	elapsed := time.Since(startTime).Round(time.Second)
	result := fmt.Sprintf("[Subagent %s] %s\n\n%s\n\nCompleted in %s", label, task, content, elapsed)
	m.notifyCompletion(sessionKey, taskID, label, channel, chatID, result)
}

func (m *SubagentManager) notifyCompletion(sessionKey, taskID, label, channel, chatID, result string) {
	slog.Info("subagent completed", "task_id", taskID, "label", label)

	m.bus.PublishInbound(context.Background(), &types.InboundMessage{
		Channel:            "system",
		SenderID:           "subagent",
		ChatID:             channel + ":" + chatID,
		Content:            result,
		Timestamp:          time.Now(),
		SessionKeyOverride: sessionKey,
		Metadata: map[string]any{
			"type":    "subagent_result",
			"task_id": taskID,
			"label":   label,
		},
	})
}
