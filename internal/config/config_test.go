package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestDefaultConfigRoundTrip(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agent.Model != "anthropic/claude-opus-4-5" {
		t.Errorf("unexpected model: %s", cfg.Agent.Model)
	}
	if cfg.Agent.MaxToolIterations != 200 {
		t.Errorf("unexpected max_tool_iterations: %d", cfg.Agent.MaxToolIterations)
	}
	if cfg.Channels.TranscriptionProvider != "groq" {
		t.Errorf("unexpected transcription_provider: %s", cfg.Channels.TranscriptionProvider)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agent.Model = "test/model"
	cfg.Agent.Provider = "openai"

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := Save(&cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Agent.Model != "test/model" {
		t.Errorf("model mismatch: got %s", loaded.Agent.Model)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("Load should not error on missing file: %v", err)
	}
	if cfg.Agent.Model != "anthropic/claude-opus-4-5" {
		t.Errorf("expected default model, got: %s", cfg.Agent.Model)
	}
}

func TestJSONCamelCaseCompat(t *testing.T) {
	input := `{
		"agent": {
			"model": "camel-model",
			"maxToolIterations": 50,
			"toolHintMaxLength": 80
		}
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(input), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Agent.Model != "camel-model" {
		t.Errorf("model: got %s", cfg.Agent.Model)
	}
	if cfg.Agent.MaxToolIterations != 50 {
		t.Errorf("maxToolIterations: got %d", cfg.Agent.MaxToolIterations)
	}
	if cfg.Agent.ToolHintMaxLength != 80 {
		t.Errorf("toolHintMaxLength: got %d", cfg.Agent.ToolHintMaxLength)
	}
}
