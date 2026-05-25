package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Seagull2ker/nanobot-go/internal/agent"
	"github.com/Seagull2ker/nanobot-go/internal/provider"
	"github.com/Seagull2ker/nanobot-go/internal/tool"
	_ "github.com/Seagull2ker/nanobot-go/internal/tool/tools"
)

func newAgentCmd() *cobra.Command {
	var message string
	var raw bool

	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Chat with the agent directly (REPL or single message)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(cmd.Context(), message, raw)
		},
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "send a single message and exit")
	cmd.Flags().BoolVar(&raw, "raw", false, "output raw text without markdown rendering")

	return cmd
}

func runAgent(ctx context.Context, message string, raw bool) error {
	cfg := mustLoadConfig()
	initLogging()

	chatModel, err := provider.BuildChatModelFromPreset("", cfg)
	if err != nil {
		return fmt.Errorf("build chat model: %w", err)
	}

	toolList := tool.Global().List()
	toolInstances := make([]tool.Tool, 0, len(toolList))
	for _, name := range toolList {
		if t, err := tool.Global().Get(name); err == nil {
			toolInstances = append(toolInstances, t)
		}
	}

	bot, err := agent.NewAgent(ctx, cfg, chatModel, toolInstances, nil, "", "", nil, nil, nil)
	if err != nil {
		return fmt.Errorf("build agent: %w", err)
	}

	if message != "" {
		reader, err := bot.ChatStream(ctx, "cli", message)
		if err != nil {
			return fmt.Errorf("chat: %w", err)
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
		fmt.Println(content)
		return nil
	}

	fmt.Println("nanobot interactive mode (type /help for commands, /quit to exit)")
	fmt.Printf("Model: %s | Tools: %d\n\n", chatModel.GetDefaultModel(), len(toolInstances))

	buf := make([]byte, 4096)
	for {
		fmt.Print("> ")
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			break
		}
		input := string(buf[:n])
		input = input[:len(input)-1]

		if input == "/quit" || input == "/exit" {
			break
		}

		fmt.Println("Processing... (full REPL coming)")
	}
	return nil
}
