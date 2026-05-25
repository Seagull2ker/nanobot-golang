package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Seagull2ker/nanobot-go/internal/config"
	"github.com/Seagull2ker/nanobot-go/internal/workspace"
)

func newOnboardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "onboard",
		Short: "Initialize configuration and workspace directory tree",
		RunE:  runOnboard,
	}
}

func runOnboard(cmd *cobra.Command, args []string) error {
	configDir, err := config.DefaultConfigDir()
	if err != nil {
		return fmt.Errorf("config dir: %w", err)
	}

	configPath, err := config.DefaultConfigPath()
	if err != nil {
		return fmt.Errorf("config path: %w", err)
	}

	// Create config directory.
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	// Write default config if not present.
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Config already exists: %s\n", configPath)
	} else {
		cfg := config.DefaultConfig()
		if err := config.Save(&cfg, ""); err != nil {
			return fmt.Errorf("save default config: %w", err)
		}
		fmt.Printf("Created config: %s\n", configPath)
	}

	// Eagerly create runtime directories.
	dirs := []string{
		config.GetPromptsDir(),
		config.GetSkillsDir(),
		config.GetWorkspacePath(""),
		config.GetSessionsDir(),
		config.GetMemoryDir(),
		config.GetMediaDir(),
		config.GetCronDir(),
		config.GetLogsDir(),
		config.GetHistoryDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	fmt.Println("Directories ready")

	// Sync embedded prompt templates into prompts/.
	if err := workspace.SyncTemplates(config.GetPromptsDir()); err != nil {
		return fmt.Errorf("sync templates: %w", err)
	}

	// Initialize MEMORY.md and HISTORY.md in memory/.
	if err := workspace.InitMemoryFiles(config.GetMemoryDir()); err != nil {
		return fmt.Errorf("init memory: %w", err)
	}

	fmt.Printf("\nAll files stored under: %s\n", configDir)
	fmt.Println("Onboarding complete! Next steps:")
	fmt.Printf("  1. Edit %s to configure your model and API key\n", configPath)
	fmt.Println("  2. Run 'nanobot gateway' to start the full server")

	return nil
}
