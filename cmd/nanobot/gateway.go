package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Seagull2ker/nanobot-go/internal/agent"
	"github.com/Seagull2ker/nanobot-go/internal/app"
	"github.com/Seagull2ker/nanobot-go/internal/bus"
	"github.com/Seagull2ker/nanobot-go/internal/channel"
	"github.com/Seagull2ker/nanobot-go/internal/command"
	"github.com/Seagull2ker/nanobot-go/internal/config"
	"github.com/Seagull2ker/nanobot-go/internal/cron"
	"github.com/Seagull2ker/nanobot-go/internal/heartbeat"
	"github.com/Seagull2ker/nanobot-go/internal/provider"
	"github.com/Seagull2ker/nanobot-go/internal/session"
	"github.com/Seagull2ker/nanobot-go/internal/subagent"
	"github.com/Seagull2ker/nanobot-go/internal/tool"
	_ "github.com/Seagull2ker/nanobot-go/internal/tool/tools"
	"github.com/Seagull2ker/nanobot-go/internal/trace"
	"github.com/Seagull2ker/nanobot-go/internal/types"
	"github.com/Seagull2ker/nanobot-go/internal/workspace"
)

func newGatewayCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gateway",
		Short: "Start the full gateway server (channels + agent + heartbeat + cron + API)",
		RunE:  runGateway,
	}
}

func runGateway(cmd *cobra.Command, args []string) error {
	cfg := mustLoadConfig()
	initLogging()

	// Initialize tracing (Langfuse via Eino callbacks).
	traceShutdown, err := trace.Init(cfg.Trace)
	if err != nil {
		return fmt.Errorf("trace init: %w", err)
	}
	defer traceShutdown()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slog.Info("nanobot gateway starting", "version", Version)

	if err = workspace.SyncTemplates(config.GetPromptsDir()); err != nil {
		return fmt.Errorf("sync templates: %w", err)
	}

	// 1. MessageBus
	messageBus := bus.NewMessageBus()

	// 2. ChatModel — skip agent assembly if no provider configured.
	var chatModel provider.ChatModelAdapter
	var bot *agent.Agent
	var loopWg sync.WaitGroup
	var toolInstances []tool.Tool

	chatModel, err = provider.BuildChatModelFromPreset(ctx, cfg)
	if err != nil {
		slog.Warn("chat model not available — agent disabled", "error", err)
		return err
	}
	slog.Info("chat model ready", "model", chatModel.GetDefaultModel())

	// 3. Tools from global registry.
	toolList := tool.Global().List()
	for _, name := range toolList {
		if t, err := tool.Global().Get(name); err == nil {
			toolInstances = append(toolInstances, t)
		}
	}
	slog.Info("tools loaded", "count", len(toolInstances))

	// 4. Sessions.
	sessions, err := session.NewSessionManager(config.GetSessionsDir())
	if err != nil {
		return fmt.Errorf("init sessions: %w", err)
	}

	// 5. Memory store.
	memStore, err := agent.NewMemoryStore(config.GetMemoryDir())
	if err != nil {
		return fmt.Errorf("init memory: %w", err)
	}

	// 6. Subagent manager.
	subagentMgr := subagent.NewSubagentManager(chatModel, toolInstances, messageBus, cfg.Agent.MaxToolIterations)

	// 7. Cron.
	cronDir := config.GetCronDir()
	cronSvc := cron.New(cronDir+"/jobs.json", nil)
	_ = cronSvc.Load()
	go cronSvc.Run(ctx)

	// 8. ReAct agent.
	bot, err = agent.NewAgent(ctx, cfg, chatModel, toolInstances, memStore, config.GetPromptsDir(), config.GetSkillsDir(), sessions, subagentMgr, cronSvc)
	if err != nil {
		return fmt.Errorf("build agent: %w", err)
	}
	bot.OnProgress = tool.NewProgressHandler(messageBus)
	slog.Info("agent built success")

	go agent.RunInboundLoop(ctx, messageBus, bot, &loopWg)

	// 9. Heartbeat.
	heartbeatService := heartbeat.StartWithBus(ctx, cfg.Heartbeat, chatModel, func(channel, chatID, content string, meta map[string]any) {
		messageBus.PublishInbound(ctx, &types.InboundMessage{
			Channel: channel, ChatID: chatID, Content: content, Metadata: meta,
		})
	})

	// 10. Command router.
	cmdRouter := command.NewRouter()
	_ = cmdRouter

	// 11. Channels — Feishu if configured, WebSocket for WebUI.
	chManager := channel.NewChannelManager(messageBus)

	if fc := cfg.Channels.Feishu; fc.AppID != "" {
		feishuCh := channel.NewFeishuChannel(channel.FeishuConfig{
			AppID:             fc.AppID,
			AppSecret:         fc.AppSecret,
			VerificationToken: fc.VerificationToken,
			EncryptKey:        fc.EncryptKey,
			AllowFrom:         fc.AllowFrom,
			GroupPolicy:       fc.GroupPolicy,
		}, messageBus)
		chManager.Register(feishuCh)
	}

	// wsChannel := channel.NewWebSocketChannel(cfg.Gateway.Host, cfg.Gateway.Port)
	// chManager.Register(wsChannel)

	if err := chManager.StartAll(ctx); err != nil {
		return fmt.Errorf("start channels: %w", err)
	}

	// Startup summary.
	fmt.Println("\n=== nanobot Gateway ===")
	fmt.Printf("Version:    %s\n", Version)
	if chatModel != nil {
		fmt.Printf("Model:      %s\n", chatModel.GetDefaultModel())
	} else {
		fmt.Println("Model:      (not configured — agent disabled)")
	}
	fmt.Printf("Tools:      %d loaded\n", len(toolInstances))
	if cfg.Channels.Feishu.AppID != "" {
		fmt.Println("Feishu:     enabled")
	}
	fmt.Println("\nAll systems ready. Press Ctrl-C to stop.")

	// Graceful shutdown orchestration.
	closeMessageBus := func() {
		messageBus.Close()
		messageBus.CloseOutbound()
	}

	<-app.StartGracefulShutdown(app.GracefulShutdownConfig{
		SigCh:              app.NewSignalChannel(),
		CancelRoot:         cancel,
		CancelAgentTasks:   bot,
		WaitGroup:          &loopWg,
		CancelSubagentTask: subagentMgr,
		CloseInbound:       closeMessageBus,
		ShutdownTimeout:    15 * time.Second,
		Components: app.RuntimeComponents{
			API:                  nil,
			Heartbeat:            heartbeatService,
			Channels:             chManager,
			ComponentStopTimeout: 5 * time.Second,
		},
	})

	return nil
}
