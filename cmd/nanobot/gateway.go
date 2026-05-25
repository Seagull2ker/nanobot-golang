package main

import (
	"context"
	"fmt"
	"log/slog"
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
	"github.com/Seagull2ker/nanobot-go/internal/tool"
	_ "github.com/Seagull2ker/nanobot-go/internal/tool/tools"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	slog.Info("nanobot gateway starting", "version", Version)

	// 1. MessageBus
	messageBus := bus.NewMessageBus()

	// 2. ChatModel — skip agent assembly if no provider configured.
	var chatModel provider.ChatModelAdapter
	var bot *agent.Agent
	var loop *agent.AgentLoop
	var toolInstances []tool.Tool

	chatModel, err := provider.BuildChatModelFromPreset("", cfg)
	if err != nil {
		slog.Warn("chat model not available — agent disabled", "error", err)
	} else {
		slog.Info("chat model ready", "model", chatModel.GetDefaultModel())

		// 3. Tools from global registry.
		toolList := tool.Global().List()
		for _, name := range toolList {
			if t, err := tool.Global().Get(name); err == nil {
				toolInstances = append(toolInstances, t)
			}
		}
		slog.Info("tools loaded", "count", len(toolInstances))

		// 4. ReAct agent.
		bot, err = agent.NewAgent(ctx, cfg, chatModel, toolInstances, nil, "", "", nil, nil, nil)
		if err != nil {
			return fmt.Errorf("build agent: %w", err)
		}
		slog.Info("agent built")

		// 5. AgentLoop.
		loop = agent.NewAgentLoop(messageBus, bot, &agent.AgentConfig{
			MaxConcurrentSessions: cfg.Agent.MaxConcurrentSubagents,
		})
		go loop.Run(ctx)
	}

	// 6. Channels — Feishu if configured, WebSocket for WebUI.
	chManager := channel.NewManager(messageBus)

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

	wsChannel := channel.NewWebSocketChannel(cfg.Gateway.Host, cfg.Gateway.Port)
	chManager.Register(wsChannel)

	if err := chManager.StartAll(ctx); err != nil {
		return fmt.Errorf("start channels: %w", err)
	}

	// 7. Cron.
	cronDir := config.GetCronDir()
	cronSvc := cron.New(cronDir+"/jobs.json", nil)
	_ = cronSvc.Load()
	go cronSvc.Run(ctx)

	// 8. Heartbeat.
	if cfg.Gateway.Heartbeat.Enabled {
		hbSvc := heartbeat.New(
			time.Duration(cfg.Gateway.Heartbeat.IntervalS)*time.Second,
			cfg.Gateway.Heartbeat.KeepRecentMessages,
			chatModel,
			config.GetPromptsDir()+"/HEARTBEAT.md",
		)
		go hbSvc.Run(ctx)
	}

	// 10. Command router.
	cmdRouter := command.NewRouter()
	_ = cmdRouter

	// Startup summary.
	fmt.Println("\n=== nanobot Gateway ===")
	fmt.Printf("Version:    %s\n", Version)
	if chatModel != nil {
		fmt.Printf("Model:      %s\n", chatModel.GetDefaultModel())
	} else {
		fmt.Println("Model:      (not configured — agent disabled)")
	}
	fmt.Printf("Tools:      %d loaded\n", len(toolInstances))
	fmt.Printf("Gateway:    %s:%d (WebSocket)\n", cfg.Gateway.Host, cfg.Gateway.Port)
	if cfg.Channels.Feishu.AppID != "" {
		fmt.Println("Feishu:     enabled")
	}
	fmt.Println("\nAll systems ready. Press Ctrl-C to stop.")

	// Graceful shutdown orchestration.
	closeInbound := func() { messageBus.Close() }

	<-app.StartGracefulShutdown(app.GracefulShutdownConfig{
		SigCh:              app.NewSignalChannel(),
		CancelRoot:         cancel,
		CancelAgentTasks:   loop,
		CancelSubagentTask: nil,
		CloseInbound:       closeInbound,
		WaitGroup:          nil,
		ShutdownTimeout:    15 * time.Second,
		Components: app.RuntimeComponents{
			Feishu:               nil,
			API:                  nil,
			ComponentStopTimeout: 5 * time.Second,
		},
	})

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	chManager.StopAll(shutdownCtx)

	return nil
}
