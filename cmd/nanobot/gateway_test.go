package main

import (
	"path/filepath"
	"testing"

	"github.com/Seagull2ker/nanobot-go/internal/config"
)

func TestGatewayConfigSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Agent.Model = "test-model"
	cfg.Agent.Provider = "test-provider"
	cfg.Channels.Feishu.AppID = "test-app-id"
	cfg.Channels.Feishu.AppSecret = "test-secret"

	if err := config.Save(&cfg, configPath); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.Agent.Model != "test-model" {
		t.Errorf("model: got %s", loaded.Agent.Model)
	}
	if loaded.Agent.Provider != "test-provider" {
		t.Errorf("provider: got %s", loaded.Agent.Provider)
	}
	if loaded.Channels.Feishu.AppID != "test-app-id" {
		t.Errorf("feishu appID: got %s", loaded.Channels.Feishu.AppID)
	}
}

func TestGatewayStartupWithoutProviderConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	cfg := config.DefaultConfig()
	cfg.Agent.Workspace = tmpDir + "/workspace"
	delete(cfg.Providers, "openai")

	if err := config.Save(&cfg, configPath); err != nil {
		t.Fatalf("save config: %v", err)
	}

	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// Config should load successfully even without provider keys.
	if p, ok := loaded.Providers["openai"]; ok && p.APIKey != "" {
		t.Error("expected empty API key")
	}
	t.Log("gateway can start without provider config")
}

func TestDefaultConfigIncludesFeishu(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Channels.Feishu.AllowFrom == nil {
		t.Error("feishu AllowFrom should have default")
	}
	if cfg.Channels.Feishu.GroupPolicy != "mention" {
		t.Errorf("feishu GroupPolicy: got %s, want mention", cfg.Channels.Feishu.GroupPolicy)
	}
}
