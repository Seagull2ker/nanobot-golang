package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Seagull2ker/nanobot-go/internal/config"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current configuration and service status",
		RunE:  runStatus,
	}
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg := mustLoadConfig()

	fmt.Println("=== nanobot Status ===")

	fmt.Println("\nModel:")
	fmt.Printf("  Model:       %s\n", cfg.Agent.Model)
	fmt.Printf("  Provider:    %s\n", cfg.Agent.Provider)
	fmt.Printf("  Max Tokens:  %d\n", cfg.Agent.MaxTokens)
	fmt.Printf("  Temperature: %.1f\n", cfg.Agent.Temperature)
	if cfg.Agent.ReasoningEffort != "" {
		fmt.Printf("  Reasoning:   %s\n", cfg.Agent.ReasoningEffort)
	}
	fmt.Printf("  Timezone:    %s\n", cfg.Agent.Timezone)

	fmt.Println("\nAgent:")
	fmt.Printf("  Workspace:               %s\n", cfg.Agent.Workspace)
	fmt.Printf("  Context Window:          %d tokens\n", cfg.Agent.ContextWindowTokens)
	fmt.Printf("  Max Tool Iterations:     %d\n", cfg.Agent.MaxToolIterations)
	fmt.Printf("  Max Concurrent Subagents: %d\n", cfg.Agent.MaxConcurrentSubagents)
	fmt.Printf("  Max Tool Result Chars:   %d\n", cfg.Agent.MaxToolResultChars)
	fmt.Printf("  Provider Retry Mode:     %s\n", cfg.Agent.ProviderRetryMode)

	fmt.Println("\nChannels:")
	fmt.Printf("  Send Progress:  %v\n", cfg.Channels.SendProgress)
	fmt.Printf("  Show Reasoning: %v\n", cfg.Channels.ShowReasoning)
	fmt.Printf("  Transcription:  %s\n", cfg.Channels.TranscriptionProvider)

	fmt.Println("\nGateway:")
	fmt.Printf("  Host: %s\n", cfg.Gateway.Host)
	fmt.Printf("  Port: %d\n", cfg.Gateway.Port)
	fmt.Printf("  Heartbeat Enabled:  %v\n", cfg.Gateway.Heartbeat.Enabled)
	fmt.Printf("  Heartbeat Interval: %ds\n", cfg.Gateway.Heartbeat.IntervalS)

	fmt.Println("\nProviders:")
	names := []string{"openai", "anthropic", "deepseek", "dashscope", "openrouter", "groq", "gemini", "ollama", "siliconflow", "zhipu"}
	for _, name := range names {
		status := "not configured"
		if p, ok := cfg.Providers[name]; ok && p.APIKey != "" {
			status = "configured"
		}
		fmt.Printf("  %s: %s\n", name, status)
	}

	if len(cfg.Tools.MCPServers) > 0 {
		fmt.Println("\nMCP Servers:")
		for name := range cfg.Tools.MCPServers {
			fmt.Printf("  %s\n", name)
		}
	}

	fmt.Println("\nPaths:")
	configDir, err := config.DefaultConfigDir()
	if err == nil {
		fmt.Printf("  Config Dir:  %s\n", configDir)
	}
	configPath, err := config.DefaultConfigPath()
	if err == nil {
		fmt.Printf("  Config File: %s\n", configPath)
	}
	fmt.Printf("  Workspace:   %s\n", cfg.Agent.Workspace)

	fmt.Println()
	return nil
}

func maskSecret(s string) string {
	if s == "" {
		return "(not set)"
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
