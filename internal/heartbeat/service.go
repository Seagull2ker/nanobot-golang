package heartbeat

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/Seagull2ker/nanobot-go/internal/config"
)

var logHB = slog.With("module", "heartbeat")

// Action is the heartbeat decision: skip or run.
type Action string

const (
	ActionSkip Action = "skip"
	ActionRun  Action = "run"
)

// HeartbeatService periodically reviews HEARTBEAT.md via LLM and executes pending tasks.
type HeartbeatService struct {
	heartbeatPath string
	model         model.ChatModel
	onExecute     func(ctx context.Context, tasks string) error
	interval      time.Duration
	stopCh        chan struct{}
}

// New creates a HeartbeatService. Call Start() to begin the ticker loop.
func New(path string, chatModel model.ChatModel, onExecute func(ctx context.Context, tasks string) error, interval time.Duration) *HeartbeatService {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	return &HeartbeatService{
		heartbeatPath: path,
		model:         chatModel,
		onExecute:     onExecute,
		interval:      interval,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the heartbeat ticker loop.
func (s *HeartbeatService) Start(ctx context.Context) {
	logHB.Info("heartbeat started", "interval", s.interval)
	ticker := time.NewTicker(s.interval)
	go func() {
		for {
			select {
			case <-ticker.C:
				s.Tick(context.Background())
			case <-s.stopCh:
				ticker.Stop()
				return
			}
		}
	}()
}

// Stop signals the heartbeat loop to exit.
func (s *HeartbeatService) Stop() { close(s.stopCh) }

// Tick performs a single heartbeat check: read HEARTBEAT.md, ask LLM to decide.
func (s *HeartbeatService) Tick(ctx context.Context) {
	content, err := os.ReadFile(s.heartbeatPath)
	if err != nil {
		if !os.IsNotExist(err) {
			logHB.Warn("read heartbeat file", "path", s.heartbeatPath, "error", err)
		}
		return
	}
	if len(content) == 0 {
		return
	}

	logHB.Debug("checking heartbeat tasks")
	action, tasks, err := s.decide(ctx, string(content))
	if err != nil {
		logHB.Warn("heartbeat decision failed", "error", err)
		return
	}
	if action != ActionRun {
		logHB.Debug("no tasks to run")
		return
	}

	logHB.Info("heartbeat tasks found, executing", "tasks", tasks)
	if s.onExecute != nil {
		if err := s.onExecute(ctx, tasks); err != nil {
			logHB.Warn("heartbeat execution failed", "error", err)
		}
	}
}

func (s *HeartbeatService) decide(ctx context.Context, content string) (Action, string, error) {
	hbTool := &schema.ToolInfo{
		Name: "heartbeat",
		Desc: "Report heartbeat decision after reviewing tasks.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"action": {Type: "string", Desc: "skip = nothing to do, run = has active tasks", Enum: []string{"skip", "run"}},
			"tasks":  {Type: "string", Desc: "Natural-language summary of active tasks (required for run)"},
		}),
	}

	messages := []*schema.Message{
		{Role: schema.System, Content: "You are a heartbeat agent. Call the heartbeat tool to report your decision."},
		{Role: schema.User, Content: "Review the following HEARTBEAT.md content and decide if any tasks need to be executed now:\n\n" + content},
	}

	if err := s.model.BindTools([]*schema.ToolInfo{hbTool}); err != nil {
		// Fallback: model doesn't support tool binding. Use content-based heuristic.
		if contentIsTask(content) {
			return ActionRun, content, nil
		}
		return ActionSkip, "", nil
	}

	resp, err := s.model.Generate(ctx, messages, model.WithToolChoice(schema.ToolChoiceForced))
	if err != nil {
		logHB.Warn("heartbeat generate failed, using heuristic", "error", err)
		if contentIsTask(content) {
			return ActionRun, content, nil
		}
		return ActionSkip, "", nil
	}

	if len(resp.ToolCalls) == 0 {
		return ActionSkip, "", nil
	}

	var result struct {
		Action string `json:"action"`
		Tasks  string `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(resp.ToolCalls[0].Function.Arguments), &result); err != nil {
		return ActionSkip, "", nil
	}

	return Action(result.Action), result.Tasks, nil
}

func contentIsTask(content string) bool {
	for _, marker := range []string{"- [ ]", "- [x]", "* ", "1. "} {
		if contains(content, marker) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// StartWithBus creates and starts a heartbeat HeartbeatService wired to the message bus.
// If cfg.Enabled is false, returns nil. The onExecute callback publishes heartbeat
// tasks as InboundMessages so the agent processes them like regular messages.
func StartWithBus(
	ctx context.Context,
	cfg config.HeartbeatConfig,
	chatModel model.ChatModel,
	publishInbound func(channel, chatID, content string, metadata map[string]any),
) *HeartbeatService {
	if !cfg.Enabled {
		return nil
	}
	interval := cfg.Interval.Duration
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	path := cfg.Path
	if path == "" {
		path = "HEARTBEAT.md"
	}

	svc := New(path, chatModel, func(ctx context.Context, tasks string) error {
		logHB.Info("heartbeat triggered", "tasks", tasks)
		publishInbound("heartbeat", "system", tasks, map[string]any{"type": "heartbeat"})
		return nil
	}, interval)
	svc.Start(ctx)
	return svc
}
