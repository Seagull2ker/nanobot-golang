package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	dataDirOnce sync.Once
	dataDir     string
)

// setDataDir caches the resolved data directory derived from the config file path.
func setDataDir(configPath string) {
	dataDir = filepath.Dir(configPath)
}

// GetDataDir returns the data directory (parent of the active config file).
// Falls back to ~/.nanobot/ if no config has been loaded yet.
func GetDataDir() string {
	dataDirOnce.Do(func() {
		if dataDir == "" {
			home, _ := os.UserHomeDir()
			dataDir = filepath.Join(home, ".nanobot")
		}
	})
	return dataDir
}

// DefaultConfigDir returns ~/.nanobot/ (cross-platform).
func DefaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".nanobot"), nil
}

// DefaultConfigPath returns ~/.nanobot/config.yaml.
func DefaultConfigPath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// ExpandWorkspace replaces leading ~/ with the user's home directory.
func ExpandWorkspace(workspace string) (string, error) {
	if strings.HasPrefix(workspace, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, workspace[2:]), nil
	}
	return workspace, nil
}

// EnsureDir creates a directory and all parents if they don't exist.
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

// runtimeSubdir returns a subdirectory within the data dir, creating it if missing.
func runtimeSubdir(name string) string {
	d := filepath.Join(GetDataDir(), name)
	_ = EnsureDir(d)
	return d
}

// GetPromptsDir returns the prompts templates directory.
func GetPromptsDir() string { return runtimeSubdir("prompts") }

// GetSkillsDir returns the skills directory.
func GetSkillsDir() string { return runtimeSubdir("skills") }

// GetSessionsDir returns the sessions storage directory.
func GetSessionsDir() string { return runtimeSubdir("sessions") }

// GetMemoryDir returns the memory storage directory.
func GetMemoryDir() string { return runtimeSubdir("memory") }

// GetMediaDir returns the media storage directory (with optional channel subdirectory).
func GetMediaDir(channel ...string) string {
	dir := runtimeSubdir("media")
	if len(channel) > 0 && channel[0] != "" {
		dir = filepath.Join(dir, channel[0])
		_ = EnsureDir(dir)
	}
	return dir
}

// GetCronDir returns the cron storage directory.
func GetCronDir() string { return runtimeSubdir("cron") }

// GetCronStorePath returns the cron jobs state file path.
func GetCronStorePath() string { return filepath.Join(GetCronDir(), "jobs.json") }

// GetLogsDir returns the logs directory.
func GetLogsDir() string { return runtimeSubdir("logs") }

// GetHistoryDir returns the CLI history directory.
func GetHistoryDir() string { return runtimeSubdir("history") }

// GetCLIHistoryPath returns the CLI command history file path.
func GetCLIHistoryPath() string { return filepath.Join(GetHistoryDir(), "cli_history") }

// GetWorkspacePath returns the workspace directory.
func GetWorkspacePath(sub string) string {
	base := runtimeSubdir("workspace")
	if sub == "" {
		return base
	}
	return filepath.Join(base, sub)
}
