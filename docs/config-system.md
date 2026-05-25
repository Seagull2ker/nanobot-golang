# 配置系统 (pkg/config/)

## 概述

配置系统负责加载、解析和管理 nanobot-eino 的所有配置项。支持 YAML/JSON/TOML 格式的配置文件 + 环境变量覆盖。

## 文件结构

```
pkg/config/
├── schema.go       # Config 结构体定义
├── loader.go       # Load/Save/config 加载与解析
├── paths.go        # 运行时目录路径解析
└── providers.go    # LLM 提供商注册表 + 自动匹配
```

---

## 1. Config 结构体 (schema.go)

`Config` 是顶层配置结构体，对应 `~/.nanobot-eino/config.yaml`:

```go
type Config struct {
    Agent     AgentConfig                // Agent 运行参数
    Providers map[string]ProviderConfig  // LLM 提供商凭据
    Channels  ChannelsConfig             // 通道配置
    Gateway   GatewayConfig              // Gateway 服务配置
    Tools     ToolsConfig                // 工具子系统配置
    Data      DataConfig                 // 数据目录（兼容旧版）
    Trace     TracingConfig              // Langfuse 追踪配置
}
```

### 1.1 AgentConfig — Agent 运行参数

```go
type AgentConfig struct {
    PromptDir           string   // 提示词目录 (SOUL.md 等)
    BuiltinSkillsDir    string   // 内置技能目录
    ContextWindowTokens int      // 上下文窗口大小 (token 数)
    MaxStep             int      // Agent 最大工具调用步数
    MaxTokens           int      // 每次 LLM 调用的最大 token 数
    Temperature         float64  // LLM 温度参数
    ReasoningEffort     string   // 推理力度 (claude 等模型支持)
    Provider            string   // 提供商名称 或 "auto"
    Model               string   // 模型名称
}
```

默认值:
- `ContextWindowTokens`: 65536
- `MaxStep`: 20
- `MaxTokens`: 8192
- `Temperature`: 0.1
- `Provider`: "auto" (自动检测)

### 1.2 ProviderConfig — LLM 提供商凭据

```go
type ProviderConfig struct {
    APIKey       string            // API 密钥
    APISecret    string            // API 密钥对（千帆等需要）
    APIBase      string            // 自定义 API 端点
    ExtraHeaders map[string]string // 额外 HTTP 头
}
```

### 1.3 ToolsConfig — 工具配置

```go
type ToolsConfig struct {
    Workspace           string      // Agent 工作空间路径
    RestrictToWorkspace bool        // 限制文件访问在工作空间内
    ExtraReadDirs       []string    // 额外可读目录
    Web                 WebConfig   // 网页工具配置
    Exec                ExecConfig  // Shell 执行安全配置
    MCP                 []MCPConfig // MCP 服务器配置
}
```

### 1.4 Duration 自定义类型

`Duration` 封装了 `time.Duration`，支持 JSON 中写字符串 `"30m"` 或数字秒数：

```go
type Duration struct {
    time.Duration
}
// MarshalJSON: "30m"
// UnmarshalJSON: "30m" 或 1800
```

---

## 2. 配置加载流程 (loader.go)

### `Load(path string) (*Config, error)`

加载流程：

```
1. viper.New() 创建新实例
2. setDefaults() — 设置所有默认值
3. configureFile() — 定位配置文件
   - path 非空: 直接用该路径
   - path 为空: 搜索 ~/.nanobot-eino/config.yaml 和 ./config.yaml
4. bindEnvVars() — 绑定环境变量覆盖
5. v.ReadInConfig() — 读取配置文件
6. v.Unmarshal(&cfg) — 反序列化为 Config 结构体
7. migrateConfig() — 兼容旧版路径迁移
```

### 环境变量映射

| 环境变量 | 配置路径 |
|----------|----------|
| `FEISHU_APP_ID` | channels.feishu.appId |
| `FEISHU_APP_SECRET` | channels.feishu.appSecret |
| `FEISHU_VERIFICATION_TOKEN` | channels.feishu.verificationToken |
| `FEISHU_ENCRYPT_KEY` | channels.feishu.encryptKey |
| `NANOBOT_PROVIDER` | agent.provider |
| `NANOBOT_AGENT_MODEL` | agent.model |
| `NANOBOT_MAX_TOKENS` | agent.maxTokens |
| `NANOBOT_TEMPERATURE` | agent.temperature |
| `NANOBOT_REASONING_EFFORT` | agent.reasoningEffort |
| `NANOBOT_WORKSPACE` | tools.workspace |

### `Save(path, cfg)` — 保存配置

将 Config 序列化为 JSON 写入文件，创建必要的父目录。

---

## 3. 路径系统 (paths.go)

所有运行时数据存储在 `~/.nanobot-eino/` 下：

```go
GetDataDir()       → ~/.nanobot-eino/
GetSessionsDir()   → ~/.nanobot-eino/sessions/
GetMemoryDir()     → ~/.nanobot-eino/memory/
GetMediaDir(ch)    → ~/.nanobot-eino/media/ 或 ~/.nanobot-eino/media/{channel}
GetCronDir()       → ~/.nanobot-eino/cron/
GetLogsDir()       → ~/.nanobot-eino/logs/
GetPromptsDir()    → ~/.nanobot-eino/prompts/
GetSkillsDir()     → ~/.nanobot-eino/skills/
GetWorkspacePath() → ~/.nanobot-eino/workspace/
GetCronStorePath() → ~/.nanobot-eino/cron/jobs.json
```

Config 结构体上的 `Resolve*` 方法支持用户通过配置文件自定义路径，否则使用上述默认值。

### 多实例支持

通过 `SetConfigPath(path)` 和 `GetConfigPath()` 支持多实例部署：
- 不同配置文件 → 不同数据目录
- 运行时目录从配置文件所在目录派生

---

## 4. LLM 提供商注册表 (providers.go)

### ProviderSpec 结构体

```go
type ProviderSpec struct {
    Name           string   // 配置键名，如 "deepseek"
    DisplayName    string   // 展示名称
    Keywords       []string // 模型名匹配关键词
    EinoType       string   // 对应 Eino SDK 的类型
    DefaultAPIBase string   // 默认 API 端点
    IsGateway      bool     // 是否网关(如 OpenRouter)
    IsLocal        bool     // 是否本地部署(如 Ollama)
    DetectByBase   string   // 通过 API Base URL 特征检测
}
```

### 已注册的提供商

| 提供商 | EinoType | 特点 |
|--------|----------|------|
| openrouter | openrouter | 网关，路由任意模型 |
| aihubmix | openai | 网关，通过 DetectByBase 检测 |
| siliconflow | siliconflow | 硅基流动 |
| anthropic | claude | Claude 系列 |
| azure_openai | openai | Azure 上的 OpenAI |
| openai | openai | GPT 系列 |
| deepseek | deepseek | DeepSeek 系列 |
| qianfan | qianfan | 百度千帆 (ERNIE) |
| dashscope | qwen | 阿里 DashScope (Qwen) |
| ark | ark | 火山引擎 Ark (豆包) |
| gemini | gemini | Google Gemini |
| moonshot | openai | Moonshot (Kimi) |
| minimax | openai | MiniMax |
| zhipu | openai | 智谱 (GLM) |
| groq | openai | Groq |
| ollama | ollama | 本地部署 |
| vllm | openai | 本地部署 |

### MatchProvider 匹配逻辑

当 `agent.provider` 为 `"auto"` 或未设置时，按优先级匹配：

1. **强制指定**: provider 非空非 auto → 直接查找
2. **模型名前缀匹配**: 如 `deepseek/xxx` → 匹配 deepseek
3. **关键词匹配**: 模型名包含 "claude" → 匹配 anthropic
4. **API Base 特征检测**: apiBase 包含 "aihubmix" → 匹配 aihubmix
5. **回退**: 第一个配置了 API Key 的提供商

---

## 5. BuildModelConfig (pkg/app/model.go)

`app.BuildModelConfig(cfg)` 将 Config 转换为 `model.Config`:

```go
func BuildModelConfig(cfg *config.Config) model.Config {
    spec, provCfg := cfg.MatchProvider("")  // 自动匹配提供商
    // 如果匹配失败，用 EffectiveProviderName 兜底
    // ...
    return model.Config{
        Type:            spec.EinoType,        // openai/claude/deepseek...
        BaseURL:         apiBase,
        APIKey:          provCfg.APIKey,
        Model:           cfg.EffectiveModel(), // agent.model 或默认 gpt-4o
        MaxTokens:       cfg.Agent.MaxTokens,
        Temperature:     cfg.Agent.Temperature,
        ReasoningEffort: cfg.Agent.ReasoningEffort,
    }
}
```

这是配置文件 (`Config`) 到模型层 (`model.Config`) 的桥梁。
