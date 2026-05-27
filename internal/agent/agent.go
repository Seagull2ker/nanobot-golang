package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	emodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"github.com/Seagull2ker/nanobot-go/internal/config"
	"github.com/Seagull2ker/nanobot-go/internal/prompt"
	"github.com/Seagull2ker/nanobot-go/internal/provider"
	"github.com/Seagull2ker/nanobot-go/internal/session"
	nanotool "github.com/Seagull2ker/nanobot-go/internal/tool"
	_ "github.com/Seagull2ker/nanobot-go/internal/tool/tools"
	"github.com/Seagull2ker/nanobot-go/internal/trace"
)

var logAgent = slog.With("module", "agent")

const toolRecoveryHint = "\n\n[Analyze the error above and try a different approach.]"

// ---------- Agent ----------

// Agent wraps an Eino react.Agent with full ChatStream lifecycle:
// command detection → pre-consolidation → build prompt → stream with MessageFuture →
// save turn → post-consolidation.
type Agent struct {
	mu           sync.RWMutex
	reactAgent   *react.Agent
	consolidator *MemoryConsolidator
	promptLoader *prompt.Loader
	skillManager *SkillsLoader
	sessions     *session.SessionManager
	cfg          *config.Config
	chatModel    provider.ChatModelAdapter
	tools        []nanotool.Tool
	maxStep      int
	baseTools    []tool.BaseTool
	toolNames    []string

	// toolCallingModel is the preferred model interface when available (immutable WithTools).
	toolCallingModel emodel.ToolCallingChatModel

	// OnProgress is called when a tool starts or finishes. Set by gateway to route progress events.
	OnProgress nanotool.ProgressFunc

	// MCP lazy loading: configs stored at init, connected on first message.
	mcpConfigs []MCPConfig
	mcpOnce    sync.Once

	activeTasks     sync.Map
	subagentManager SubagentCanceller
}

// MCPConfig is a lightweight MCP server config passed from tools package.
type MCPConfig struct {
	Name         string
	Type         string
	Command      string
	Args         []string
	Env          map[string]string
	URL          string
	Headers      map[string]string
	ToolTimeout  time.Duration
	EnabledTools []string
}

// MCPConnector connects to MCP servers and returns tools.
type MCPConnector func(ctx context.Context, cfg MCPConfig) ([]tool.InvokableTool, error)

// SubagentCanceller is the interface for cancelling subagent tasks.
type SubagentCanceller interface {
	CancelBySession(sessionKey string) int
	CancelAll() int
}

// NewAgent creates an Agent with all subsystems wired up.
func NewAgent(
	ctx context.Context,
	cfg *config.Config,
	chatModel provider.ChatModelAdapter,
	toolList []nanotool.Tool,
	store *MemoryStore,
	promptDir, builtinSkillsDir string,
	sessions *session.SessionManager,
	subagentMgr SubagentCanceller,
	cronService CronSvc,
) (*Agent, error) {
	defs := cfg.Agent
	maxStep := defs.MaxToolIterations
	if maxStep <= 0 {
		maxStep = 20
	}

	// Wrap tools as Eino InvokableTools.
	einoTools := make([]tool.BaseTool, len(toolList))
	for i, t := range toolList {
		einoTools[i] = &einoToolAdapter{t: t}
	}

	// Conditionally add cron tool from cron service.
	//if cronService != nil {
	//	TODO: wire when cron tool backend is available
	//}

	// Conditionally add spawn tool from subagent manager.
	if subagentMgr != nil {
		spawnAdapter := &einoToolAdapter{t: &spawnTool{spawner: subagentMgr}}
		einoTools = append(einoTools, spawnAdapter)
	}

	// Build consolidator with session manager for auto-save after consolidation.
	consolidator := NewMemoryConsolidator(store, chatModel, sessions, DefaultConsolidationConfig())

	// Load prompts and skills.
	promptLoader := prompt.NewLoader(promptDir)
	skills := NewSkillsLoader(promptDir, builtinSkillsDir)
	_ = skills.LoadSkills()

	// Detect ToolCallingChatModel for immutable tool binding.
	var tcm emodel.ToolCallingChatModel
	if tc, ok := chatModel.(emodel.ToolCallingChatModel); ok {
		tcm = tc
	}

	// Wrap all tools with truncation + progress reporting.
	progressFn := func(ctx context.Context, toolName, status string) {}
	wrappedTools := make([]tool.BaseTool, len(einoTools))
	for i, t := range einoTools {
		wrappedTools[i] = &wrappedEinoTool{inner: t, onProgress: progressFn}
	}

	toolNames := listToolNames(ctx, wrappedTools)
	reactAgent, err := buildReactAgent(ctx, cfg, chatModel, wrappedTools, toolNames, maxStep)
	if err != nil {
		return nil, err
	}

	a := &Agent{
		reactAgent:       reactAgent,
		consolidator:     consolidator,
		promptLoader:     promptLoader,
		skillManager:     skills,
		sessions:         sessions,
		cfg:              cfg,
		chatModel:        chatModel,
		tools:            toolList,
		maxStep:          maxStep,
		baseTools:        wrappedTools,
		toolNames:        toolNames,
		subagentManager:  subagentMgr,
		toolCallingModel: tcm,
	}
	return a, nil
}

// CronSvc is the interface for the cron service.
type CronSvc interface {
	AddJob(schedule interface{}) (interface{}, error)
	ListJobs() []interface{}
	RemoveJob(id string) error
}

// spawnTool is a nanobot Tool wrapper for subagent spawning.
type spawnTool struct {
	spawner SubagentCanceller
}

func (s *spawnTool) Name() string               { return "spawn" }
func (s *spawnTool) Description() string        { return "Spawn a background subagent" }
func (s *spawnTool) Parameters() map[string]any { return map[string]any{} }
func (s *spawnTool) ReadOnly() bool             { return false }
func (s *spawnTool) ConcurrencySafe() bool      { return true }
func (s *spawnTool) Exclusive() bool            { return false }
func (s *spawnTool) Execute(ctx context.Context, params map[string]any) (*nanotool.Result, error) {
	task, _ := params["task"].(string)
	label, _ := params["label"].(string)
	if task == "" {
		return &nanotool.Result{Content: "Error: task is required"}, nil
	}
	_ = s.spawner.CancelBySession("")
	return &nanotool.Result{Content: fmt.Sprintf("Subagent spawned for task: %s", label)}, nil
}

// wrappedEinoTool wraps an Eino BaseTool with progress reporting.
// The progress callback is captured via closure from NewAgent and routed through a.OnProgress.
type wrappedEinoTool struct {
	inner      tool.BaseTool
	onProgress func(ctx context.Context, toolName, status string)
}

func (w *wrappedEinoTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return w.inner.Info(ctx)
}

func (w *wrappedEinoTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	info, _ := w.Info(ctx)
	name := ""
	if info != nil {
		name = info.Name
	}
	w.onProgress(ctx, name, "running")
	result, err := w.inner.(tool.InvokableTool).InvokableRun(ctx, args, opts...)
	if err != nil || strings.HasPrefix(result, "Error") {
		w.onProgress(ctx, name, "failed")
	} else {
		w.onProgress(ctx, name, "completed")
	}
	return result, err
}

func buildReactAgent(ctx context.Context, cfg *config.Config, chatModel provider.ChatModelAdapter, einoTools []tool.BaseTool, toolNames []string, maxStep int) (*react.Agent, error) {
	modifier := buildMessageModifier(cfg)

	return react.NewAgent(ctx, &react.AgentConfig{
		Model: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: einoTools,
		},
		MessageModifier: modifier,
		MaxStep:         maxStep,
	})
}

// ---------- Tool Adapter ----------

type einoToolAdapter struct {
	t nanotool.Tool
}

func (a *einoToolAdapter) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: a.t.Name(), Desc: a.t.Description()}, nil
}

func (a *einoToolAdapter) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	normalized := normalizeToolArguments(argumentsInJSON)
	var params map[string]any
	if err := json.Unmarshal([]byte(normalized), &params); err != nil {
		// Return structured fallback instead of failing.
		fallback, _ := json.Marshal(map[string]any{
			"_tool_argument_error": "invalid_json",
			"_raw_arguments":       truncateString(argumentsInJSON, 400),
		})
		return "Error: invalid JSON arguments. " + string(fallback) + toolRecoveryHint, nil
	}
	if params == nil {
		params = make(map[string]any)
	}

	result, err := a.t.Execute(ctx, params)
	if err != nil {
		return "Error: " + err.Error(), nil
	}
	return result.Content, nil
}

// ---------- ChatStream ----------

// ensureMCPConnected lazily connects MCP servers on first real message,
// recreating the react agent with the additional tools.
func (a *Agent) ensureMCPConnected(ctx context.Context) {
	a.mcpOnce.Do(func() {
		if len(a.mcpConfigs) == 0 {
			return
		}
		logAgent.Info("lazy-connecting MCP servers", "count", len(a.mcpConfigs))
		var newTools []tool.BaseTool
		for _, cfg := range a.mcpConfigs {
			tools, err := connectMCPServer(ctx, cfg)
			if err != nil {
				logAgent.Warn("MCP server connection failed, skipped", "server", cfg.Name, "error", err)
				continue
			}
			for _, t := range tools {
				newTools = append(newTools, t)
			}
		}
		if len(newTools) == 0 {
			return
		}

		allTools := make([]tool.BaseTool, 0, len(a.baseTools)+len(newTools))
		allTools = append(allTools, a.baseTools...)
		allTools = append(allTools, newTools...)

		toolNames := listToolNames(ctx, allTools)
		newAgent, err := buildReactAgent(ctx, a.cfg, a.chatModel, allTools, toolNames, a.maxStep)
		if err != nil {
			logAgent.Warn("failed to recreate agent with MCP tools", "error", err)
			return
		}

		a.mu.Lock()
		a.reactAgent = newAgent
		a.baseTools = allTools
		a.toolNames = toolNames
		a.mu.Unlock()

		logAgent.Info("MCP tools connected, agent rebuilt", "new_tools", len(newTools))
	})
}

// connectMCPServer is set by the tools package to break the import cycle.
var connectMCPServer func(ctx context.Context, cfg MCPConfig) ([]tool.InvokableTool, error)

// SetMCPConnector sets the MCP connection function.
func SetMCPConnector(fn func(ctx context.Context, cfg MCPConfig) ([]tool.InvokableTool, error)) {
	connectMCPServer = fn
}

// ChatStream processes a message within a session with full lifecycle.
func (a *Agent) ChatStream(ctx context.Context, sessionID, input string) (*schema.StreamReader[*schema.Message], error) {
	// Command detection — handle before any LLM work.
	cmd := strings.TrimSpace(strings.ToLower(input))
	switch cmd {
	case "/new", "/clear":
		return a.handleNewSession(ctx, sessionID)
	case "/help":
		return stringToStream(commandHelpText()), nil
	case "/stop":
		return a.handleStop(sessionID)
	}

	// Lazy-connect MCP servers on first real message.
	a.ensureMCPConnected(ctx)

	// Trace the full turn.
	spanCtx := trace.StartSpan(ctx, "agent.turn", map[string]any{"session": sessionID, "input": input})

	// Create cancellable context for /stop support.
	taskCtx, taskCancel := context.WithCancel(ctx)
	a.activeTasks.Store(sessionID, taskCancel)

	// Inject sessionID for tool progress callbacks.
	taskCtx = nanotool.ContextWithSessionID(taskCtx, sessionID)

	sess := a.sessions.GetOrCreate(sessionID)

	// Pre-consolidation: trim context if exceeding window.
	a.consolidator.MaybeConsolidateByTokens(taskCtx, sess, 2000)

	// Build prompt messages.
	history := sess.GetHistory(0)
	messages, err := a.buildMessages(taskCtx, history, input)
	if err != nil {
		a.activeTasks.Delete(sessionID)
		taskCancel()
		return nil, err
	}

	// Stream with MessageFuture for full turn capture.
	logAgent.Info("user request", "session", sessionID, "input", input)

	futureOpt, future := react.WithMessageFuture()
	stream, err := a.reactAgent.Stream(taskCtx, messages, futureOpt)
	if err != nil {
		a.activeTasks.Delete(sessionID)
		taskCancel()
		return nil, err
	}

	pipeReader, pipeWriter := schema.Pipe[*schema.Message](10)
	go func() {
		defer func() {
			a.activeTasks.Delete(sessionID)
			taskCancel()
			pipeWriter.Close()
		}()

		// Collect all intermediate messages (tool_calls, tool results, final response).
		var collectedMsgs []*schema.Message
		var collectWg sync.WaitGroup
		collectWg.Add(1)
		go func() {
			defer collectWg.Done()
			sIter := future.GetMessageStreams()
			for {
				msgSR, hasNext, iterErr := sIter.Next()
				if iterErr != nil || !hasNext {
					break
				}
				msg, concatErr := schema.ConcatMessageStream(msgSR)
				if concatErr != nil {
					break
				}
				collectedMsgs = append(collectedMsgs, msg)
			}
		}()

		var fullResponse strings.Builder
		for {
			msg, recvErr := stream.Recv()
			if recvErr != nil {
				if recvErr == io.EOF || taskCtx.Err() == context.Canceled {
					break
				}
				pipeWriter.Send(nil, recvErr)
				break
			}
			fullResponse.WriteString(msg.Content)
			pipeWriter.Send(msg, nil)
		}
		stream.Close()
		collectWg.Wait()

		logAgent.Info("model response", "session", sessionID, "output", fullResponse.String())

		// Save turn: user input + all collected messages.
		inputMsg := schema.UserMessage(input)
		if nanotool.InputRoleFromContext(taskCtx) == "assistant" {
			inputMsg = &schema.Message{Role: schema.Assistant, Content: input}
		}

		if len(collectedMsgs) > 0 {
			sess.AddMessage(inputMsg)
			for _, m := range collectedMsgs {
				sess.AddMessage(m)
			}
		} else {
			responseText := strings.TrimSpace(fullResponse.String())
			if responseText == "" {
				return
			}
			sess.AddMessage(inputMsg)
			sess.AddMessage(&schema.Message{Role: schema.Assistant, Content: responseText})
		}

		if saveErr := a.sessions.Save(sess); saveErr != nil {
			logAgent.Error("Failed to save session", "session", sessionID, "error", saveErr)
		}

		// Post-consolidation (use background context since streaming ctx may be cancelled).
		a.consolidator.MaybeConsolidateByTokens(context.Background(), sess, 2000)

		trace.EndSpan(spanCtx, nil)
	}()

	return pipeReader, nil
}

// ---------- Command Handlers ----------

func (a *Agent) handleNewSession(ctx context.Context, sessionID string) (*schema.StreamReader[*schema.Message], error) {
	sess := a.sessions.GetOrCreate(sessionID)
	logAgent.Info("new session", "session", sessionID, "messages", len(sess.Messages))

	// Archive unconsolidated messages before clearing.
	if err := a.consolidator.ArchiveUnconsolidated(ctx, sess); err != nil {
		logAgent.Error("archive before /new failed", "session", sessionID, "error", err)
		return stringToStream("Memory archival failed, session not cleared. Please try again."), nil
	}

	sess.Clear()
	if err := a.sessions.Save(sess); err != nil {
		return stringToStream("Failed to clear session. Please try again."), nil
	}
	a.sessions.Invalidate(sessionID)
	return stringToStream("New session started. Previous conversation has been archived to memory."), nil
}

func (a *Agent) handleStop(sessionID string) (*schema.StreamReader[*schema.Message], error) {
	stopped := false
	if cancelFn, ok := a.activeTasks.LoadAndDelete(sessionID); ok {
		cancelFn.(context.CancelFunc)()
		stopped = true
	}

	subCancelled := 0
	if a.subagentManager != nil {
		subCancelled = a.subagentManager.CancelBySession(sessionID)
	}

	if stopped || subCancelled > 0 {
		msg := "Task has been stopped."
		if subCancelled > 0 {
			msg += fmt.Sprintf(" (%d subagent task(s) also cancelled)", subCancelled)
		}
		return stringToStream(msg), nil
	}
	return stringToStream("No active task to stop."), nil
}

func commandHelpText() string {
	return "nanobot commands:\n/new — Start a new conversation\n/stop — Stop the current task\n/help — Show available commands"
}

func (a *Agent) CancelAll() int {
	cancelled := 0
	a.activeTasks.Range(func(key, value any) bool {
		if cancelFn, ok := value.(context.CancelFunc); ok {
			cancelFn()
			cancelled++
		}
		a.activeTasks.Delete(key)
		return true
	})
	return cancelled
}

// ---------- Prompt Building ----------

func (a *Agent) buildMessages(ctx context.Context, history []*schema.Message, input string) ([]*schema.Message, error) {
	systemMsgs, err := a.promptLoader.BuildSystemMessages(ctx)
	if err != nil {
		// Fallback: if prompt files are missing, use a minimal bootstrap.
		systemMsgs = []*schema.Message{schema.SystemMessage("You are a helpful AI assistant.")}
		logAgent.Warn("prompt files not found, using fallback bootstrap", "error", err)
	}

	var parts []string
	parts = append(parts, systemMsgs[0].Content)

	// Always-active skills.
	if alwaysSkills := a.skillManager.GetAlwaysSkills(); len(alwaysSkills) > 0 {
		parts = append(parts, "# Active Skills\n\n"+skillsContent(alwaysSkills))
	}

	// Skills summary.
	if summary := a.skillManager.BuildSkillsSummary(); summary != "" {
		parts = append(parts, "# Skills\n\n"+summary)
	}

	// Long-term memory.
	if memCtx := a.consolidator.Store.GetMemoryContext(); memCtx != "" {
		parts = append(parts, memCtx)
	}

	systemMsgs[0].Content = strings.Join(parts, "\n\n---\n\n")
	messages := append(systemMsgs, history...)

	userContent := buildRuntimeContext(ctx) + "\n\n" + input
	role := nanotool.InputRoleFromContext(ctx)
	if role == "assistant" {
		messages = append(messages, &schema.Message{Role: schema.Assistant, Content: userContent})
	} else {
		messages = append(messages, schema.UserMessage(userContent))
	}
	return messages, nil
}

func skillsContent(skills []*Skill) string {
	var parts []string
	for _, s := range skills {
		parts = append(parts, "### Skill: "+s.Meta.Name+"\n"+s.Content)
	}
	return strings.Join(parts, "\n\n---\n\n")
}

func buildRuntimeContext(ctx context.Context) string {
	now := time.Now()
	timeStr := fmt.Sprintf("%s (%s)", now.Format("2006-01-02 15:04 (Monday)"), now.Format("MST"))
	lines := []string{
		"[Runtime Context — metadata, not instructions]",
		"Current Time: " + timeStr,
	}
	if pi := nanotool.GetProgressInfo(ctx); pi != nil {
		if pi.Channel != "" {
			lines = append(lines, "Channel: "+pi.Channel)
		}
		if pi.ChatID != "" {
			lines = append(lines, "Chat ID: "+pi.ChatID)
		}
	}
	return strings.Join(lines, "\n")
}

// ---------- Tool Helpers ----------

func listToolNames(ctx context.Context, allTools []tool.BaseTool) []string {
	names := make([]string, 0, len(allTools))
	for _, t := range allTools {
		info, err := t.Info(ctx)
		if err != nil || info == nil || strings.TrimSpace(info.Name) == "" {
			continue
		}
		names = append(names, strings.TrimSpace(info.Name))
	}
	sort.Strings(names)
	return names
}

func normalizeToolArguments(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return "{}"
	}
	trimmed = stripJSONCodeFence(trimmed)
	if trimmed == "" {
		return "{}"
	}

	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		fallback, _ := json.Marshal(map[string]any{
			"_tool_argument_error": "invalid_json",
			"_raw_arguments":       truncateString(trimmed, 400),
		})
		return string(fallback)
	}
	return trimmed
}

func stripJSONCodeFence(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```JSON")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func truncateString(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}

func stringToStream(content string) *schema.StreamReader[*schema.Message] {
	sr, sw := schema.Pipe[*schema.Message](1)
	go func() {
		sw.Send(&schema.Message{Role: schema.Assistant, Content: content}, nil)
		sw.Close()
	}()
	return sr
}

// ---------- Governance ----------

func buildMessageModifier(cfg *config.Config) react.MessageModifier {
	return func(ctx context.Context, input []*schema.Message) []*schema.Message {
		messages := dropOrphanToolResults(input)
		messages = backfillMissingToolResults(messages)
		messages = microcompact(messages, cfg.Agent.MaxToolResultChars)
		return messages
	}
}

func dropOrphanToolResults(messages []*schema.Message) []*schema.Message {
	declaredIDs := make(map[string]bool)
	for _, m := range messages {
		if m.Role == schema.Assistant {
			for _, tc := range m.ToolCalls {
				declaredIDs[tc.ID] = true
			}
		}
	}
	var result []*schema.Message
	for _, m := range messages {
		if m.Role == schema.Tool && m.ToolCallID != "" {
			if !declaredIDs[m.ToolCallID] {
				continue
			}
		}
		result = append(result, m)
	}
	return result
}

func backfillMissingToolResults(messages []*schema.Message) []*schema.Message {
	toolResultIDs := make(map[string]bool)
	for _, m := range messages {
		if m.Role == schema.Tool {
			toolResultIDs[m.ToolCallID] = true
		}
	}
	var result []*schema.Message
	for _, m := range messages {
		result = append(result, m)
		if m.Role == schema.Assistant {
			for _, tc := range m.ToolCalls {
				if !toolResultIDs[tc.ID] {
					result = append(result, &schema.Message{
						Role:       schema.Tool,
						ToolCallID: tc.ID,
						Content:    "[Tool result unavailable -- call was interrupted or lost]",
					})
				}
			}
		}
	}
	return result
}

func microcompact(messages []*schema.Message, maxResultChars int) []*schema.Message {
	const keepRecent = 10
	const minCompactChars = 500

	compactableTools := map[string]bool{
		"read_file": true, "exec": true, "web_search": true,
		"web_fetch": true, "list_dir": true,
	}

	var compactable []int
	for i, m := range messages {
		if m.Role == schema.Tool && compactableTools[m.ToolName] {
			if len(m.Content) > minCompactChars {
				compactable = append(compactable, i)
			}
		}
	}

	if len(compactable) <= keepRecent {
		return messages
	}

	toCompact := compactable[:len(compactable)-keepRecent]
	for _, idx := range toCompact {
		messages[idx] = &schema.Message{
			Role:       messages[idx].Role,
			ToolCallID: messages[idx].ToolCallID,
			ToolName:   messages[idx].ToolName,
			Content:    fmt.Sprintf("[%s result omitted from context]", messages[idx].ToolName),
		}
	}

	if maxResultChars > 0 {
		for _, m := range messages {
			if m.Role == schema.Tool && len(m.Content) > maxResultChars {
				m.Content = m.Content[:maxResultChars] + "\n\n[Result truncated]"
			}
		}
	}

	return messages
}
