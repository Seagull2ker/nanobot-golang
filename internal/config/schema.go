// Package config provides the configuration schema and loader for nanobot.
// Maps 17 Pydantic models from the Python backend to Go structs.
package config

import (
	"path/filepath"
	"time"
)

// FeishuChannelConfig holds Feishu (Lark) channel credentials.
// These come from the Feishu Open Platform app settings.
type FeishuChannelConfig struct {
	AppID             string   `json:"appId" yaml:"app_id"`
	AppSecret         string   `json:"appSecret" yaml:"app_secret"`
	VerificationToken string   `json:"verificationToken" yaml:"verification_token"`
	EncryptKey        string   `json:"encryptKey,omitempty" yaml:"encrypt_key,omitempty"`
	AllowFrom         []string `json:"allowFrom,omitempty" yaml:"allow_from,omitempty"`
	GroupPolicy       string   `json:"groupPolicy,omitempty" yaml:"group_policy,omitempty"`
}

// ChannelsConfig holds chat channel settings.
type ChannelsConfig struct {
	SendProgress          bool                `json:"sendProgress" yaml:"send_progress"`
	SendToolHints         bool                `json:"sendToolHints" yaml:"send_tool_hints"`
	ShowReasoning         bool                `json:"showReasoning" yaml:"show_reasoning"`
	SendMaxRetries        int                 `json:"sendMaxRetries" yaml:"send_max_retries"`
	TranscriptionProvider string              `json:"transcriptionProvider" yaml:"transcription_provider"`
	TranscriptionLanguage string              `json:"transcriptionLanguage,omitempty" yaml:"transcription_language,omitempty"`
	Feishu                FeishuChannelConfig `json:"feishu" yaml:"feishu"`
}

// DefaultChannelsConfig returns the default channel configuration.
func DefaultChannelsConfig() ChannelsConfig {
	return ChannelsConfig{
		SendProgress:          true,
		SendToolHints:         false,
		ShowReasoning:         true,
		SendMaxRetries:        3,
		TranscriptionProvider: "groq",
		Feishu: FeishuChannelConfig{
			AppID:       "",
			AppSecret:   "",
			AllowFrom:   []string{"*"},
			GroupPolicy: "mention",
		},
	}
}

// DreamConfig holds memory consolidation settings.
type DreamConfig struct {
	IntervalH        int    `json:"intervalH" yaml:"interval_h"`
	Cron             string `json:"cron,omitempty" yaml:"cron,omitempty"`
	ModelOverride    string `json:"modelOverride,omitempty" yaml:"model_override,omitempty"`
	MaxBatchSize     int    `json:"maxBatchSize" yaml:"max_batch_size"`
	MaxIterations    int    `json:"maxIterations" yaml:"max_iterations"`
	AnnotateLineAges bool   `json:"annotateLineAges" yaml:"annotate_line_ages"`
}

// DefaultDreamConfig returns the default dream configuration.
func DefaultDreamConfig() DreamConfig {
	return DreamConfig{
		IntervalH:        2,
		MaxBatchSize:     20,
		MaxIterations:    15,
		AnnotateLineAges: true,
	}
}

// InlineFallbackConfig is one inline fallback model configuration.
type InlineFallbackConfig struct {
	Model               string  `json:"model" yaml:"model"`
	Provider            string  `json:"provider" yaml:"provider"`
	MaxTokens           int     `json:"maxTokens,omitempty" yaml:"max_tokens,omitempty"`
	ContextWindowTokens int     `json:"contextWindowTokens,omitempty" yaml:"context_window_tokens,omitempty"`
	Temperature         float64 `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	ReasoningEffort     string  `json:"reasoningEffort,omitempty" yaml:"reasoning_effort,omitempty"`
}

// FallbackCandidate is either a preset name (string) or inline config.
type FallbackCandidate struct {
	Name   string                `json:"-" yaml:"-"`
	Inline *InlineFallbackConfig `json:"-" yaml:"-"`
}

// IsInline reports whether this is an inline config (vs a named preset reference).
func (f FallbackCandidate) IsInline() bool { return f.Inline != nil }

// AgentConfig holds agent behavior and model configuration.
type AgentConfig struct {
	Workspace              string      `json:"workspace" yaml:"workspace"`
	Model                  string      `json:"model" yaml:"model"`
	Provider               string      `json:"provider" yaml:"provider"`
	MaxTokens              int         `json:"maxTokens" yaml:"max_tokens"`
	ContextWindowTokens    int         `json:"contextWindowTokens" yaml:"context_window_tokens"`
	Temperature            float64     `json:"temperature" yaml:"temperature"`
	ReasoningEffort        string      `json:"reasoningEffort,omitempty" yaml:"reasoning_effort,omitempty"`
	ProviderRetryMode      string      `json:"providerRetryMode" yaml:"provider_retry_mode"`
	MaxToolIterations      int         `json:"maxToolIterations" yaml:"max_tool_iterations"`
	MaxConcurrentSubagents int         `json:"maxConcurrentSubagents" yaml:"max_concurrent_subagents"`
	MaxToolResultChars     int         `json:"maxToolResultChars" yaml:"max_tool_result_chars"`
	ToolHintMaxLength      int         `json:"toolHintMaxLength" yaml:"tool_hint_max_length"`
	Timezone               string      `json:"timezone" yaml:"timezone"`
	BotName                string      `json:"botName" yaml:"bot_name"`
	BotIcon                string      `json:"botIcon" yaml:"bot_icon"`
	MaxMessages            int         `json:"maxMessages" yaml:"max_messages"`
	ConsolidationRatio     float64     `json:"consolidationRatio" yaml:"consolidation_ratio"`
	Dream                  DreamConfig `json:"dream" yaml:"dream"`
}

func DefaultAgentConfig() AgentConfig {
	configDir, _ := DefaultConfigDir()
	return AgentConfig{
		Workspace:              filepath.Join(configDir, "workspace"),
		Model:                  "anthropic/claude-opus-4-5",
		Provider:               "auto",
		MaxTokens:              8192,
		ContextWindowTokens:    65536,
		Temperature:            0.1,
		ProviderRetryMode:      "standard",
		MaxToolIterations:      200,
		MaxConcurrentSubagents: 1,
		MaxToolResultChars:     16000,
		ToolHintMaxLength:      40,
		Timezone:               "UTC",
		BotName:                "nanobot",
		BotIcon:                "🐈",
		MaxMessages:            120,
		ConsolidationRatio:     0.5,
		Dream:                  DefaultDreamConfig(),
	}
}

// ProviderConfig holds LLM provider connection settings.
type ProviderConfig struct {
	APIKey       string            `json:"apiKey,omitempty" yaml:"api_key,omitempty"`
	APIBase      string            `json:"apiBase,omitempty" yaml:"api_base,omitempty"`
	ExtraHeaders map[string]string `json:"extraHeaders,omitempty" yaml:"extra_headers,omitempty"`
	ExtraBody    map[string]any    `json:"extraBody,omitempty" yaml:"extra_body,omitempty"`
}

// BedrockProviderConfig extends ProviderConfig with AWS-specific fields.
type BedrockProviderConfig struct {
	ProviderConfig `json:"" yaml:""`
	Region         string `json:"region,omitempty" yaml:"region,omitempty"`
	Profile        string `json:"profile,omitempty" yaml:"profile,omitempty"`
}

// ProvidersConfig maps provider name to its configuration.
// Keys are provider names like "openai", "deepseek", "anthropic", etc.
type ProvidersConfig map[string]ProviderConfig

// ApiConfig holds OpenAI-compatible API server settings.
type ApiConfig struct {
	Host    string  `json:"host" yaml:"host"`
	Port    int     `json:"port" yaml:"port"`
	Timeout float64 `json:"timeout" yaml:"timeout"`
}

func DefaultApiConfig() ApiConfig {
	return ApiConfig{Host: "127.0.0.1", Port: 8900, Timeout: 120.0}
}

// Duration wraps time.Duration for JSON serialization as a human-readable string (e.g. "30m", "5s").
type Duration struct {
	time.Duration
}

// HeartbeatConfig holds heartbeat service settings.
type HeartbeatConfig struct {
	Enabled  bool     `json:"enabled" yaml:"enabled"`
	Path     string   `json:"path"`
	Interval Duration `json:"interval"`
}

// DefaultHeartbeatConfig returns the default heartbeat configuration.
func DefaultHeartbeatConfig() HeartbeatConfig {
	return HeartbeatConfig{
		Enabled:  false,
		Path:     "HEARTBEAT.md",
		Interval: Duration{30 * time.Minute},
	}
}

// CronGatewayConfig configures persistence for the cron gateway.
type CronGatewayConfig struct {
	StorePath string `json:"storePath"`
}

func DefaultCronGatewayConfig() CronGatewayConfig {
	return CronGatewayConfig{
		StorePath: "",
	}
}

// GatewayConfig holds gateway/server settings.
type GatewayConfig struct {
	Cron CronGatewayConfig `json:"cron" yaml:"cron"`
}

// DefaultGatewayConfig returns the default gateway configuration.
func DefaultGatewayConfig() GatewayConfig {
	return GatewayConfig{}
}

// MCPServerConfig holds MCP server connection settings.
type MCPServerConfig struct {
	Name         string            `json:"name" yaml:"name"`
	Type         string            `json:"type,omitempty" yaml:"type,omitempty"`
	Command      string            `json:"command" yaml:"command"`
	Args         []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env          map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	URL          string            `json:"url" yaml:"url"`
	Headers      map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	ToolTimeout  int               `json:"toolTimeout" yaml:"tool_timeout"`
	EnabledTools []string          `json:"enabledTools" yaml:"enabled_tools"`
}

// DefaultMCPServerConfig returns the default MCP server configuration.
func DefaultMCPServerConfig() MCPServerConfig {
	return MCPServerConfig{
		ToolTimeout:  30,
		EnabledTools: []string{"*"},
	}
}

// ToolsConfig holds tool-specific configuration.
type ToolsConfig struct {
	RestrictToWorkspace bool                       `json:"restrictToWorkspace" yaml:"restrict_to_workspace"`
	SSRFWhitelist       []string                   `json:"ssrfWhitelist,omitempty" yaml:"ssrf_whitelist,omitempty"`
	MCPServers          map[string]MCPServerConfig `json:"mcpServers,omitempty" yaml:"mcp_servers,omitempty"`
}

// DefaultToolsConfig returns the default tools configuration.
func DefaultToolsConfig() ToolsConfig {
	return ToolsConfig{}
}

// DataConfig is kept for backward compatibility with existing config files.
// New code should use config.GetSessionsDir() / config.GetMemoryDir() instead.
type DataConfig struct {
	Dir       string `json:"dir"`
	MemoryDir string `json:"memoryDir"`
}

// TracingConfig holds Langfuse tracing settings.
type TracingConfig struct {
	Enabled   bool   `json:"enabled"`
	Endpoint  string `json:"endpoint"`
	PublicKey string `json:"publicKey"`
	SecretKey string `json:"secretKey"`
}

// Config is the root configuration for nanobot.
type Config struct {
	Agent     AgentConfig     `json:"agent" yaml:"agent"`
	Channels  ChannelsConfig  `json:"channels" yaml:"channels"`
	Providers ProvidersConfig `json:"providers" yaml:"providers"`
	API       ApiConfig       `json:"api" yaml:"api"`
	Gateway   GatewayConfig   `json:"gateway" yaml:"gateway"`
	Heartbeat HeartbeatConfig `json:"heartbeat" yaml:"heartbeat"`
	Tools     ToolsConfig     `json:"tools" yaml:"tools"`
	Data      DataConfig      `json:"data" yaml:"data"`
	Trace     TracingConfig   `json:"trace" yaml:"trace"`
}

// DefaultConfig returns a Config with all defaults populated.
func DefaultConfig() Config {
	return Config{
		Agent:     DefaultAgentConfig(),
		Channels:  DefaultChannelsConfig(),
		Gateway:   DefaultGatewayConfig(),
		Heartbeat: DefaultHeartbeatConfig(),
		Tools:     DefaultToolsConfig(),
	}
}

// ConfigError is returned when a config value is missing or invalid.
type ConfigError struct {
	Field string
	Value string
}

func (e *ConfigError) Error() string {
	return "config: " + e.Field + " " + e.Value + " not found"
}
