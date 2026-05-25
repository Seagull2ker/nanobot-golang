package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Load reads config.json and returns the configuration.
// Priority: env var > config file > defaults.
func Load(path string) (*Config, error) {
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return nil, fmt.Errorf("config: default path: %w", err)
		}
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("json")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok && !os.IsNotExist(err) {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
	}

	cfg := DefaultConfig()
	if err := v.Unmarshal(&cfg, viper.DecoderConfigOption(func(dc *mapstructure.DecoderConfig) {
		dc.TagName = "json"
		dc.WeaklyTypedInput = true
	})); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	expanded, err := ExpandWorkspace(cfg.Agent.Workspace)
	if err != nil {
		return nil, fmt.Errorf("config: expand workspace: %w", err)
	}
	cfg.Agent.Workspace = expanded

	// Track active config path so all runtime dirs derive from it.
	setDataDir(path)

	return &cfg, nil
}

// Save writes the config to disk, creating parent directories as needed.
func Save(cfg *Config, path string) error {
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("config: default path: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("config: create tmp: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("config: fsync: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("config: close tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: rename: %w", err)
	}
	return nil
}
