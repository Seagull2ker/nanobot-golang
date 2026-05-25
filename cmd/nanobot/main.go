package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/Seagull2ker/nanobot-go/internal/config"
	"github.com/Seagull2ker/nanobot-go/internal/log"
)

var (
	// Version is the build version, overridden via -ldflags at build time.
	Version = "dev"
	cfgFile string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "nanobot",
		Short: "AI agent with tools, memory, and channels",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			switch cmd.Name() {
			case "onboard", "version", "help":
				return nil
			}
			return nil
		},
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ~/.nanobot/config.json)")

	viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config"))

	rootCmd.AddCommand(
		newGatewayCmd(),
		newAgentCmd(),
		newOnboardCmd(),
		newStatusCmd(),
		newVersionCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func mustLoadConfig() *config.Config {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}
	return cfg
}

func initLogging() {
	log.Configure("info")
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("nanobot %s\n", Version)
		},
	}
}
