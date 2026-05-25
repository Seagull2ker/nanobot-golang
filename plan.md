# nanobot → Go/Eino 开发计划

## 概述

用 Golang + cloudwego/eino 框架重新实现 nanobot 后端，保留现有 React/TypeScript WebUI，通过 WebSocket 对接。

### 与 Python 版的关键差异

- Eino 框架的 `ChatModelAgent` 提供内置 ReAct 循环（think → act → observe），**但 governance（消息修复、token 预算管理、安全分类）需要通过 Eino 中间件扩展实现**，不是完全不用写
- Provider 保留 registry + factory 模式，先实现 4 个主流 provider（OpenAI、Anthropic、DeepSeek、DashScope）
- Channel 优先只做飞书，其余平台后续按需添加
- Dream 两阶段记忆处理保留
- MCP 工具保留
- 子 Agent（SubAgent）保留

---

## 技术选型

| 用途 | 选型 | 理由 |
|------|------|------|
| AI Agent 框架 | cloudwego/eino v0.8.13 | 内置 ChatModelAgent（ReAct Graph）、ChatModel 接口、Tool 接口、中间件体系、checkpoint/resume、子 Agent |
| CLI | cobra + viper | cobra 命令路由，viper 配置管理（JSON + env override） |
| 配置 | viper | JSON 文件加载 + NANOBOT_ 环境变量绑定 + pflag 集成 |
| WebSocket | gorilla/websocket | 与 WebUI 通信 |
| YAML | gopkg.in/yaml.v3 | SKILL.md frontmatter 解析 |
| MCP | mark3labs/mcp-go | MCP 工具集成 |
| 重试 | cenkalti/backoff | LLM API 调用的指数退避 |
| 断路器 | sony/gobreaker | Provider 故障转移 |
| Token 计数 | tiktoken-go | Snip 历史时的 token 预算计算 |
| 日志 | log/slog | Go 标准库结构化日志 |
| HTTP | net/http | Provider SDK 之外的通用请求 |
| Cron | robfig/cron/v3 | 定时任务（Cron 工具 + Heartbeat） |
| Git | go-git | Dream 行龄标注 |

---

## Eino ChatModelAgent 能力分析与适配策略

### 调查结论

通过深入阅读 Eino v0.8.13 的 `adk/` 包源码（`react.go`、`chatmodel.go`、`handler.go`、`wrappers.go`、`flow.go`、`interrupt.go`、`runner.go`），得出以下结论：

**Eino ChatModelAgent 已内置的能力（不需要手写）：**

| 能力 | Eino 实现 |
|------|----------|
| ReAct 循环 | `newReact()` 构建 compose.Graph：Init → ChatModel → ToolNode → loop back 或 END |
| 最大迭代次数 | `MaxIterations` 配置（默认 20），超限返回 `ErrExceedMaxIterations` |
| Return-Directly 工具 | `ReturnDirectly` map，工具执行后直接返回结果，不再继续循环 |
| 流式处理 | `EnableStreaming` + `AsyncIterator[AgentEvent]`，支持 streaming 和 non-streaming |
| Checkpoint/Resume | `Runner` + `CheckPointStore`，gob 序列化状态，支持中断恢复 |
| 模型重试 | `ModelRetryConfig`（MaxRetries + IsRetryAble + BackoffFunc），指数退避 100ms-10s |
| 子 Agent | `OnSetSubAgents`/`OnSetAsSubAgent` 接口 + `transfer_to_agent` 内置工具 + `flowAgent` 路由 |
| Agent 工具化 | `NewAgentTool()` 将任意 Agent 包装为 Tool |
| Workflow 编排 | `SequentialAgent`、`ParallelAgent`、`LoopAgent`、`Supervisor`、`PlanExecute` |
| 会话状态 | `AddSessionValue`/`GetSessionValue` + `SetRunLocalValue`/`GetRunLocalValue`（支持 checkpoint 持久化） |
| 回调 | `WithCallbacks()` + `AgentCallbackInput`/`AgentCallbackOutput` |
| 历史重写 | `HistoryRewriter` 跨 Agent 消息角色转换 |

**Eino 不自动处理但可通过中间件扩展的能力：**

| Python AgentRunner 功能 | Eino 扩展点 | 适配方案 |
|---|---|---|
| Orphan tool_result 清理 | `ChatModelAgentMiddleware.BeforeModelRewriteState` | Governance 中间件：扫描 messages，删除无匹配 tool_calls 声明的 tool 消息 |
| Backfill 缺失 tool_result | 同上 | Governance 中间件：为无结果 tool_calls 插入占位 tool 消息 |
| Microcompact（旧工具结果→占位符） | 同上 | Governance 中间件：超过 10 个 compactable 工具结果后，替换旧内容为占位符 |
| Snip 历史（token 预算裁剪） | 同上 | Governance 中间件：tiktoken 估算 → 前端截断 → 保留 system 消息 → user-first 修正 |
| 空响应重试（最多 2 次） | AgentLoop 层 | AgentLoop 检测空响应 → 重新调用 agent.Generate()；最终兜底：无工具 re-prompt |
| Length truncation 恢复（最多 3 次） | `ChatModelAgentMiddleware.WrapModel` | WrapModel 检测 finish_reason=length → 追加恢复消息 → 触发继续 |
| 注入处理（中轮消息注入） | AgentLoop 层 | AgentLoop 维护注入队列，通过 Eino 的 interrupt/resume 机制中轮注入 |
| 工具安全分类（SSRF/workspace/repeated） | `ChatModelAgentMiddleware.WrapInvokableToolCall` | 工具错误分类中间件：检测错误文本 → 返回针对性软错误消息 |
| 工具结果规范化（非空/持久化/截断） | `ChatModelAgentMiddleware.WrapInvokableToolCall` + reduction 中间件 | 后处理工具结果：确保非空、过大持久化到文件、超限截断 |
| Reasoning/Thinking 提取 | `ChatModelAgentMiddleware.WrapModel` | 多源提取（reasoning_content 字段 → thinking_blocks → think 标签） |

### 架构决策：Eino ChatModelAgent + 自定义中间件 + AgentLoop

```
┌─────────────────────────────────────────────────┐
│                  AgentLoop                       │
│  (消息分发 + injection + empty/length recovery)   │
│                                                  │
│  ┌────────────────────────────────────────────┐ │
│  │        Eino ChatModelAgent                  │ │
│  │        (内置 ReAct Graph)                    │ │
│  │                                              │ │
│  │  ┌──────────────────────────────────────┐   │ │
│  │  │  Governance Middleware                │   │ │
│  │  │  (BeforeModelRewriteState)            │   │ │
│  │  │  - orphan cleanup + backfill          │   │ │
│  │  │  - microcompact + snip                │   │ │
│  │  └──────────────────────────────────────┘   │ │
│  │  ┌──────────────────────────────────────┐   │ │
│  │  │  Tool Safety Middleware               │   │ │
│  │  │  (WrapInvokableToolCall)              │   │ │
│  │  │  - SSRF/workspace/repeated            │   │ │
│  │  │  - result normalization               │   │ │
│  │  └──────────────────────────────────────┘   │ │
│  │  ┌──────────────────────────────────────┐   │ │
│  │  │  Thinking Middleware                   │   │ │
│  │  │  (WrapModel)                          │   │ │
│  │  │  - thinking extraction                │   │ │
│  │  │  - length recovery                    │   │ │
│  │  └──────────────────────────────────────┘   │ │
│  └────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────┘
```

---

## 目录结构

```
nanobot-golang/
├── cmd/nanobot/
│   ├── main.go                # CLI 根命令（cobra + viper）
│   ├── version.go             #   version 子命令
│   ├── onboard.go             #   onboard 子命令（初始化配置和工作区）
│   ├── status.go              #   status 子命令（显示配置和状态）
│   ├── agent.go               #   agent 子命令（交互式聊天/单次消息）
│   └── gateway.go             #   gateway 子命令（启动完整服务器）
├── internal/
│   ├── config/                   # 配置 schema + 加载器
│   │   ├── schema.go             #   所有配置 struct
│   │   ├── loader.go             #   JSON 加载/保存 + env override
│   │   └── paths.go              #   路径工具
│   ├── errors/                   # 结构化错误系统
│   │   └── errors.go
│   ├── log/                      # slog 配置
│   │   └── log.go
│   ├── types/                    # 共享类型
│   │   ├── messages.go           #   InboundMessage, OutboundMessage
│   │   ├── tools.go              #   ToolCallRequest, LLMResponse
│   │   └── sessions.go           #   Session
│   ├── bus/                      # MessageBus
│   │   └── bus.go
│   ├── session/                  # SessionManager
│   │   └── manager.go
│   ├── provider/                 # LLM Provider 实现
│   │   ├── provider.go           #   ChatModel 接口适配
│   │   ├── registry.go           #   ProviderSpec 注册表
│   │   ├── factory.go            #   Provider 工厂
│   │   ├── openai.go             #   OpenAI ChatModel
│   │   ├── anthropic.go          #   Anthropic/Claude ChatModel
│   │   ├── deepseek.go           #   DeepSeek ChatModel
│   │   ├── dashscope.go          #   通义千问 (DashScope) ChatModel
│   │   ├── fallback.go           #   故障转移 + 断路器
│   │   └── retry.go              #   重试逻辑
│   ├── tool/                     # Tool 实现
│   │   ├── tool.go               #   Tool 接口
│   │   ├── registry.go           #   ToolRegistry（注册 + 发现）
│   │   ├── schema_types.go       #   Schema 类型（Str/Int/Bool/Arr/Obj 等）
│   │   ├── sandbox.go            #   SandboxBackend 接口 + Local 实现
│   │   ├── file_state.go         #   文件变更追踪
│   │   ├── path_utils.go         #   路径安全
│   │   └── tools/                #   具体工具实现
│   │       ├── shell.go          #     Shell 执行
│   │       ├── filesystem.go     #     文件读写/编辑/列表
│   │       ├── web.go            #     URL 抓取 + 搜索
│   │       ├── cron.go           #     定时任务
│   │       ├── mcp.go            #     MCP 工具
│   │       ├── apply_patch.go    #     代码补丁
│   │       ├── notebook.go       #     Notebook 编辑
│   │       ├── spawn.go          #     子 Agent 生成
│   │       └── message.go        #     消息发送
│   ├── agent/                    # Agent 业务层
│   │   ├── loop.go               #   AgentLoop（消息分发 + injection）
│   │   ├── agent.go              #   ReAct Agent 组装（Eino ChatModelAgent 配置）
│   │   ├── runner.go             #   AgentRunner（Agent 执行 + empty/length recovery）
│   │   ├── middleware/           #   Eino 中间件
│   │   │   ├── governance.go     #     Governance 中间件（orphan/backfill/microcompact/snip）
│   │   │   ├── tool_safety.go    #     工具安全中间件（SSRF/workspace/repeated）
│   │   │   └── thinking.go       #     Thinking 提取中间件
│   │   ├── memory.go             #   MemoryStore + Consolidator
│   │   ├── dream.go              #   Dream 两阶段记忆处理
│   │   ├── context.go            #   ContextBuilder（系统提示词）
│   │   ├── skills.go             #   SkillsLoader
│   │   ├── hook.go               #   AgentHook 生命周期接口
│   │   └── progress.go           #   ProgressHook（流式进度推送）
│   ├── channel/                  # Channel
│   │   ├── base.go               #   Channel 接口
│   │   ├── manager.go            #   生命周期管理
│   │   ├── feishu.go             #   飞书
│   │   └── websocket.go          #   WebSocket（WebUI 通信）
│   ├── api/                      # API Server
│   │   └── server.go             #   OpenAI 兼容 HTTP API
│   ├── cron/                     # CronService
│   │   └── service.go
│   ├── heartbeat/                # HeartbeatService
│   │   └── service.go
│   ├── command/                  # CommandRouter
│   │   └── router.go
│   └── security/                 # 安全
│       └── ssrf.go               #   SSRF 检测 + 路径安全
├── pkg/nanobot.go                # 公共 SDK facade
├── templates/                    # 内嵌 prompt 模板
├── plan.md
├── go.mod
├── Makefile
├── .gitignore
└── .golangci.yml
```

---

## 阶段依赖关系

```
Phase 0: 基础设施 ✅
    │
    ├── Phase 1: Provider ──┐
    └── Phase 2: Tools ─────┤
                             ▼
                      Phase 3: Agent Core
                      (Eino Agent 组装 + 中间件 + Memory + Dream)
                             │
                             ▼
                      Phase 4: Channel + 子系统
                      (飞书 + WebSocket + Cron + Heartbeat + Command + API)
                             │
                             ▼
                      Phase 5: CLI + 打磨
```

---

## Phase 0：基础设施 ✅ 已完成

**依赖：** 无 | **预估：** 2-3 天 | **状态：** ✅ 完成

### 0.1 Go Module 初始化 ✅
- `go.mod`（`github.com/nanobot-ai/nanobot-golang`，Go 1.22）
- `Makefile`、`.gitignore`、`.golangci.yml`
- 目录骨架

### 0.2 结构化错误系统 ✅
- 文件：`internal/errors/errors.go`
- `Kind` 分类：unknown、invalid、unauthorized、not_found、rate_limited、unavailable、timeout、canceled、network、context_too_long、max_steps
- `Error` 结构体（Kind + Op + StatusCode + Public + Err）
- `Wrap()`、`Normalize()`、`Retryable()`、`KindOf()`

### 0.3 日志 ✅
- 文件：`internal/log/log.go`
- `Configure(level)` — JSON handler（debug/info/warn/error）

### 0.4 共享类型 ✅
- `internal/types/messages.go` — InboundMessage、OutboundMessage
- `internal/types/tools.go` — ToolCallRequest、LLMResponse
- `internal/types/sessions.go` — Session

### 0.5 MessageBus ✅
- 文件：`internal/bus/bus.go`
- inbound/outbound buffered channels（容量 100）
- 非阻塞 Publish（context + select），sync.Once 安全 Close

### 0.6 配置系统 ✅
- `internal/config/schema.go` — 17 个配置 struct（含 ProvidersConfig、ModelPresetConfig、DreamConfig、MCPServerConfig 等）
- `internal/config/loader.go` — viper 加载（JSON 文件 + NANOBOT_ 环境变量自动绑定 + 默认值回退）
- `internal/config/paths.go` — 路径工具（DefaultConfigDir、GetPromptsDir、GetSkillsDir、GetSessionsDir 等 10+ 路径辅助函数）
- 7 个测试全部通过

### 验证 ✅
- `go build ./...` 通过
- `go vet ./...` 零警告
- `go test ./...` 7/7 通过

---

## Phase 1：Provider 子系统 ✅ 完成（2026-05-24）

**依赖：** Phase 0 | **预估：** 7-10 天 | **实际：** 1 天
**目标：** 4 个主流 LLM 可通过统一接口调用，支持 registry 自动匹配和 factory 创建

### 核心设计

保留 registry + factory 模式（用户明确要求），但 scope 缩小到 4 个主流 provider + 必要的 fallback：

```
用户配置 (config.json)
    │
    ▼
Factory (BuildProvider)
    │
    ▼
Registry (ProviderSpec 注册表)
    │
    ├── OpenAI Compat (openai, deepseek, dashscope)
    ├── Anthropic
    └── ... (后续扩展)
    │
    ▼
ChatModel 适配器 (Eino 兼容接口)
    │
    ▼
HTTP API 调用 (net/http)
```

### 1.1 Provider 接口定义

**文件：** `internal/provider/provider.go`

提供与 Eino `model.BaseChatModel` 兼容的接口适配层：

```go
// ChatModelAdapter 包装 Eino 的 BaseChatModel 接口，增加我们需要的元数据方法
type ChatModelAdapter interface {
    model.BaseChatModel  // Eino 标准接口：Generate() + Stream()
    GetDefaultModel() string
    EstimatePromptTokens(messages []*schema.Message) int
    SupportsThinking() bool
}
```

### 1.2 Provider 注册表

**文件：** `internal/provider/registry.go`

```go
type BackendType string

const (
    BackendOpenAICompat BackendType = "openai_compat"  // OpenAI、DeepSeek、DashScope 共用
    BackendAnthropic    BackendType = "anthropic"
)

type ProviderSpec struct {
    Name            string              // 如 "openai"、"deepseek"
    Backend         BackendType         // 实现类型
    DefaultModel    string              // 默认模型名
    DefaultAPIBase  string              // 默认 API 地址
    Models          []string            // 支持的模型列表
    SupportsThinking bool               // 是否支持 extended thinking
    ThinkingStyle   string              // thinking 注入方式
}

// Registry 管理所有已注册的 ProviderSpec
type Registry struct { ... }
func (r *Registry) Register(spec ProviderSpec)
func (r *Registry) Get(name string) (ProviderSpec, error)
func (r *Registry) Match(model string) (ProviderSpec, error)  // 根据模型名自动匹配
func (r *Registry) List() []ProviderSpec
```

预注册 4 个 provider：

| Name | Backend | DefaultModel | 说明 |
|------|---------|-------------|------|
| openai | openai_compat | gpt-4o | OpenAI Chat Completions API |
| anthropic | anthropic | claude-opus-4-5 | Anthropic Messages API |
| deepseek | openai_compat | deepseek-chat | DeepSeek API（OpenAI 兼容） |
| dashscope | openai_compat | qwen-max | 通义千问 DashScope API（OpenAI 兼容） |

### 1.3 Provider 工厂

**文件：** `internal/provider/factory.go`

```go
// BuildChatModel 根据 provider 名称和配置创建 ChatModelAdapter
func BuildChatModel(ctx context.Context, name string, cfg ProviderConfig, preset ModelPresetConfig) (ChatModelAdapter, error)

// BuildChatModelFromPreset 从预设和配置自动选择 provider
func BuildChatModelFromPreset(ctx context.Context, presetName string, cfg *Config) (ChatModelAdapter, error)
```

工厂内部逻辑：
1. 从 Registry 查找 provider spec（按名称或模型自动匹配）
2. 根据 Backend 类型创建对应的适配器实例
3. 注入 API key、API base、extra headers
4. 包装重试逻辑
5. 返回 ChatModelAdapter

### 1.4 OpenAI 兼容适配器

**文件：** `internal/provider/openai.go`

- 实现 ChatModelAdapter 接口
- OpenAI Chat Completions API 格式（messages → JSON → HTTP POST）
- 流式：SSE 解析（`data: [DONE]` 终止）
- 工具调用：`tools` 参数 → `function` 类型 tool_calls
- 错误分类：HTTP 状态码 + error type → 结构化 Error
- 支持 `reasoning_effort` 参数（OpenAI o-series 模型）
- **此适配器同时服务 OpenAI、DeepSeek、DashScope**（通过配置不同的 APIBase 和认证方式）

### 1.5 Anthropic 适配器

**文件：** `internal/provider/anthropic.go`

- 实现 ChatModelAdapter 接口
- Anthropic Messages API 格式：
  - 系统提示词在顶层 `system` 字段（非 messages[0]）
  - Content blocks（text + tool_use + tool_result）
- Thinking/Extended Thinking：`thinking` 参数 + `budget_tokens`
- 流式：SSE（message_start → content_block_start → content_block_delta → content_block_stop → message_delta → message_stop）
- 图像支持：`source` block（base64 + media_type）

### 1.6 DeepSeek 适配器

**文件：** `internal/provider/deepseek.go`

- 复用 OpenAI 兼容适配器类
- 差异点：
  - 默认 API base：`https://api.deepseek.com`
  - Thinking 注入：DeepSeek-R1 使用 `thinking: {type: "enabled"}` 参数
  - 某些错误格式略有不同

### 1.7 DashScope（通义千问）适配器

**文件：** `internal/provider/dashscope.go`

- 复用 OpenAI 兼容适配器类
- 差异点：
  - 默认 API base：`https://dashscope.aliyuncs.com/compatible-mode/v1`
  - 认证 header 格式
  - qwen-plus/qwen-max 的 reasoning 支持

### 1.8 重试与故障转移

**文件：** `internal/provider/retry.go`、`internal/provider/fallback.go`

**重试（retry.go）：**
- `standard` 模式：3 次重试，指数退避（1s → 2s → 4s）
- `persistent` 模式：无限重试（最大 60s 间隔，连续 10 次相同错误停止）
- 瞬时错误分类：
  - 429（rate limit）：可重试
  - 429（quota exceeded）：不可重试
  - 503/502/504：可重试
  - 网络超时：可重试

**故障转移（fallback.go）：**
- 断路器：连续 3 次失败 → 60s 冷却期
- Fallback 链：主 provider → fallback_models 列表（按顺序尝试）
- 每个 fallback 候选可以是预设名称或内联配置
- 断路器的半开状态检测

### 验证
- 每个 provider 的 Generate + Stream 集成测试（go-vcr 录制 HTTP 响应）
- 错误场景测试（401/429/503/超时）
- 重试逻辑测试（失败→恢复→成功）
- 断路器测试（3 次失败→打开→半开→恢复）
- Fallback 链测试（主 provider 不可用 → 自动切换）
- Registry 匹配测试（根据模型名正确匹配 provider）

---

## Phase 2：Tool 子系统

**依赖：** Phase 0 | **预估：** 5-7 天
**目标：** 所有工具实现统一接口，可在 ReAct Agent 中注册和使用，支持 MCP 工具

### 2.1 工具接口

**文件：** `internal/tool/tool.go`

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() *Schema           // JSON Schema 参数定义
    Execute(ctx context.Context, params map[string]any) (*ToolResult, error)
    ReadOnly() bool                // 只读工具可在 approval 模式跳过审批
    ConcurrencySafe() bool         // 是否可与其他工具并发执行
    Exclusive() bool               // 是否需要独占执行（如 shell）
}

type ToolResult struct {
    Content string
    Error   error
}
```

### 2.2 Schema 类型系统

**文件：** `internal/tool/schema_types.go`

```go
type SchemaElement interface {
    ToJSONSchema() map[string]any
    Validate(value any, path string) error
}

// 具体类型：Str, Int, Num, Bool, Arr, Obj, Any, Union
```

每个类型实现 JSON Schema 生成和参数校验。

### 2.3 工具注册表

**文件：** `internal/tool/registry.go`

```go
type ToolRegistry struct { ... }

func (r *ToolRegistry) Register(t Tool)
func (r *ToolRegistry) Get(name string) (Tool, error)
func (r *ToolRegistry) List() []Tool
func (r *ToolRegistry) PrepareCall(name string, params map[string]any) (Tool, map[string]any, error)
func (r *ToolRegistry) ToEinoTools() []tool.BaseTool  // 转换为 Eino 工具列表
```

- 内置工具按名称排序在前，MCP 工具在后
- `PrepareCall()`：查找工具 → 类型转换参数 → 校验
- Go 无 pkgutil 自动发现 → 每个工具通过 `init()` 自注册到全局 Registry

### 2.4 需要实现的工具

**优先级 1（必须）：**

1. **ShellTool**（`tool/tools/shell.go`）
   - `command`（必填）、`workdir`（可选）、`timeout`（可选，默认 120s）、`background`（可选）
   - 沙箱化执行（LocalSandboxBackend）
   - stdout/stderr 分离，exit code 返回
   - **命令安全**：deny patterns 拦截危险命令（rm -rf / format / dd / mkfs / shutdown / fork bomb 等）+ allow patterns 白名单 + 内网 URL 检测 + workspace 外路径拒绝

2. **ReadFileTool**（`tool/tools/filesystem.go`）
   - `file_path`（必填）、`offset`（可选，默认 1）、`limit`（可选，默认 2000）
   - 路径安全检查 + cat -n 格式输出
   - **maxChars=128K 上限**：超大文件截断 + 续读提示（提示用户用 offset/limit 翻页）

3. **WriteFileTool**（`tool/tools/filesystem.go`）
   - `file_path`（必填）、`content`（必填）
   - 路径安全检查 + 自动创建父目录

4. **EditFileTool**（`tool/tools/filesystem.go`）
   - `file_path`（必填）、`old_string`（必填）、`new_string`（必填）、`replace_all`（可选）
   - 精确字符串替换 + **空格容错回退**（归一化空白后重匹配）+ 多次出现保护
   - **相似度诊断**：未找到时用 LCS 定位最佳匹配位置，显示行级 diff 和相似度百分比

5. **ListFilesTool**（`tool/tools/filesystem.go`）
   - `path`（可选）、`recursive`（可选，默认 false）、`max_entries`（可选，默认 200）
   - **Noise-dir 跳过**：默认忽略 .git / node_modules / __pycache__ / .venv / venv / dist / build 等 10+ 目录
   - 排序输出，目录加后缀 `/`，文件显示大小

**优先级 2（重要）：**

6. **WebFetchTool**（`tool/tools/web.go`）
   - `url`（必填）、`prompt`（可选，内容提取指令）、`max_chars`（可选，默认 50000）
   - **SSRF 防护**：DNS 解析后逐 IP 检查 CIDR（10 个内网/链路本地/多播段）+ metadata 端点屏蔽 + localhost/.local 拒绝
   - **HTML → Markdown 转换**：链接/a/strong/em/heading/列表标签转 Markdown，剥离 script/style
   - **双阶段抓取**：尝试 Jina Reader 优先（LLM 友好），失败回退直接抓取
   - 最多 5 次重定向，每次重定向后重新 SSRF 校验
   - 非信任内容横幅标注（`[External content]`）

7. **WebSearchTool**（`tool/tools/web.go`）
   - `query`（必填）、`count`（可选，默认 5，最大 10）
   - **至少支持 DuckDuckGo**（免费，无需 API key）：Instant Answer API → HTML 抓取回退
   - 可扩展：Brave / Tavily / SearXNG / Jina（按需配置 API key）

8. **CronTool**（`tool/tools/cron.go`）
   - 定时任务管理（create/list/delete）
   - robfig/cron/v3 后端

9. **MCPTool**（`tool/tools/mcp.go`）
   - MCP 服务器连接管理（mark3labs/mcp-go）
   - 工具发现 + 代理执行
   - 配置文件中的 mcpServers 映射

10. **ApplyPatchTool**（`tool/tools/apply_patch.go`）
    - 代码补丁应用

11. **MessageTool**（`tool/tools/message.go`）
    - Agent 通过工具向用户发送消息

12. **SpawnTool**（`tool/tools/spawn.go`）
    - 子 Agent 生成（通过 Eino 的 NewAgentTool 包装）

### 2.5 沙箱后端

**文件：** `internal/tool/sandbox.go`

```go
type SandboxBackend interface {
    Run(ctx context.Context, command string, opts *SandboxOptions) (*SandboxResult, error)
}

type SandboxOptions struct {
    CWD     string
    Env     []string
    Timeout time.Duration
}

type SandboxResult struct {
    Stdout   string
    Stderr   string
    ExitCode int
    TimedOut bool
}
```

- `LocalSandboxBackend`：使用 `os/exec` 本地执行
- 通过 `context.WithTimeout` 控制超时
- Docker 沙箱暂不实现

### 2.6 路径安全

**文件：** `internal/tool/path_utils.go`

- `ResolveFilePath(workspace, filePath)` → 绝对路径
- `ValidatePathSafety(path, workspace)` → 目录遍历 + 符号链接检查
- `EnsureWithinWorkspace(path, workspace)` → 路径必须在工作区内

### 2.7 文件变更追踪

**文件：** `internal/tool/file_state.go`

- 跨工具调用的文件变更状态追踪
- `RecordWrite()`、`RecordEdit()`、`GetState()`、`HasChanged()`
- 供后续工具调用判断文件是否已在当前轮次被修改

### 2.8 工具结果包装器（Tool Wrapper）

**文件：** `internal/tool/wrapper.go`

参考 nanobot-go `wrapper.go`，在工具执行层做统一后处理（与 Phase 3 中间件互补）：

- `WrapTool(tool Tool, maxChars int, onProgress ProgressFunc) Tool` — 装饰器模式
- **截断**：超过 `maxChars`（默认 16000）的结果从中间截断，保留开头和结尾
- **进度回调**：`ToolProgressFunc(ctx, toolName, status)` — running/completed/failed
- **错误降级**：工具返回 error → 转为 `"Error: ..."` 字符串（不中断 Agent 流程）
- **失败提示**：Error 结果末尾追加 `[Analyze the error above and try a different approach.]`

### 验证
- 每个工具的 valid/invalid 参数测试
- Schema 校验测试
- 路径安全边界测试（../ 逃逸、符号链接攻击）
- Sandbox 超时行为测试
- Shell deny patterns 拦截测试
- SSRF DNS rebinding 测试
- Edit 空格容错匹配测试
- MCP 工具发现和执行测试

---

## Phase 3：Agent Core

**依赖：** Phase 1-2 | **预估：** 7-10 天
**目标：** 完整的消息处理流程，含 Eino 自定义中间件、Memory、Dream、Context、SubAgent

### 3.1 ReAct Agent 组装

**文件：** `internal/agent/agent.go`

使用 Eino ChatModelAgent + 自定义中间件组装 Agent：

```go
func BuildAgent(ctx context.Context, cfg *config.Config, provider provider.ChatModelAdapter, tools []tool.BaseTool, session *Session) (*adk.ChatModelAgent, error) {
    // 1. 构建系统提示词
    systemPrompt := buildSystemPrompt(ctx, cfg, session)

    // 2. 组装工具列表（builtin + MCP + skills）
    allTools := assembleTools(tools, session)

    // 3. 构建中间件链（按优先级从外到内注册）
    middlewares := []adk.ChatModelAgentMiddleware{
        middleware.NewGovernance(cfg),      // Governance（最外层：消息修复）
        middleware.NewToolSafety(),          // 工具安全
        middleware.NewThinking(),            // Thinking 提取（最内层：包裹 Model）
    }

    // 4. 创建 ChatModelAgent
    agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
        Name:        "nanobot",
        Description: "nanobot AI assistant",
        Instruction: systemPrompt,
        Model:       provider,
        ToolsConfig: adk.ToolsConfig{
            Tools: allTools,
        },
        MaxIterations: cfg.Agents.Defaults.MaxToolIterations,
        Handlers:      middlewares,
        ModelRetryConfig: &adk.ModelRetryConfig{
            MaxRetries: 3,
        },
    })

    // 5. 配置子 Agent（如果存在）
    if subAgents := buildSubAgents(ctx, cfg, provider); len(subAgents) > 0 {
        agent.OnSetSubAgents(ctx, subAgents)
    }

    return agent, nil
}
```

### 3.2 自定义 Eino 中间件

#### 3.2.1 Governance 中间件

**文件：** `internal/agent/middleware/governance.go`

实现 `ChatModelAgentMiddleware` 接口的 `BeforeModelRewriteState` 方法。

**处理流程（每次 LLM 调用前执行）：**
1. **Orphan 清理**：扫描 messages，删除 role=tool 但没有对应 assistant tool_calls 声明的消息
2. **Backfill**：为声明了 tool_calls 但无对应 tool 结果的 assistant 消息插入占位 tool 消息
3. **Microcompact**：超过 10 个 compactable 工具结果后，用占位符替换旧内容（仅当原内容 > 500 字符）
4. **Snip**：tiktoken 估算 token 数 → 超出预算时前端截断 → 保留 system 消息 → user-first 修正

**常量配置：**
- `maxEmptyRetries` = 2（空响应最大重试）
- `maxLengthRecoveries` = 3（长度截断最大恢复）
- `microcompactKeepRecent` = 10（保留最近 N 个 compactable 工具结果）
- `microcompactMinChars` = 500（触发 compact 的最小字符数）
- `snipSafetyBuffer` = 1024（token 预算安全缓冲区）

#### 3.2.2 工具安全中间件

**文件：** `internal/agent/middleware/tool_safety.go`

实现 `ChatModelAgentMiddleware` 接口的 `WrapInvokableToolCall` / `WrapStreamableToolCall` 方法。

**安全检查（每次工具调用前/后）：**
1. **SSRF 检测**：检查 web_fetch URL 是否是内部/私有地址 → 软错误消息（告诉 LLM 这是安全边界）
2. **Workspace 违规**：检测路径遍历 / 工作区外文件访问 → 第 2 次同一路径违规后升级为详细错误
3. **重复外部查找**：同一 URL/query 的 web_fetch/web_search 超过 2 次 → 阻止调用
4. **结果规范化**：确保 tool result 非空 → 过大持久化到文件 → 超限截断

#### 3.2.3 Thinking 提取中间件

**文件：** `internal/agent/middleware/thinking.go`

实现 `ChatModelAgentMiddleware` 接口的 `WrapModel` 方法。

**处理流程（LLM 响应后）:**
1. 提取 reasoning_content 字段（DeepSeek-R1/Kimi/MiMo 等）
2. 提取 Anthropic thinking_blocks
3. 提取内联 `<think>` / `<thought>` 标签
4. 剥离所有 think 标签，输出干净 content
5. 通过 hook 发送 reasoning 事件

### 3.3 AgentLoop

**文件：** `internal/agent/loop.go`

不再包含 LLM 交互循环（由 Eino ChatModelAgent 处理），专注于消息分发和生命周期管理：

```go
type AgentLoop struct {
    bus            *bus.MessageBus
    sessions       *session.SessionManager
    contextBuilder *ContextBuilder
    cfg            *config.Config
    provider       provider.ChatModelAdapter
    tools          []tool.BaseTool
    hooks          []AgentHook
    semaphore      chan struct{}  // 跨 session 并发控制
}

func (l *AgentLoop) Run(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case msg := <-l.bus.ConsumeInbound():
            go l.handleMessage(msg)
        }
    }
}
```

**AgentLoop 特有的处理（Eino 不负责的）：**

1. **Injection 处理**：
   - 维护 per-session 注入队列（`asyncio.Queue` 等价物 → `chan *InboundMessage`，容量 20）
   - 在执行 agent.Run() 之前检查队列，将新消息注入到 messages 末尾
   - 最大 3 条/轮，最大 5 轮注入循环
   - Role alternation：如最后一条消息已是 user，则合并内容

2. **空响应恢复**（在 AgentLoop 层处理，不在中间件中）：
   - 调用 agent.Run() → 检测空结果 → 最多 2 次重新调用
   - 最终兜底：无工具的 re-prompt → "Please provide your response..."

3. **Length 截断恢复**：
   - 检测 agent 输出的 finish_reason → 追加继续消息 → 重新调用 agent.Run()
   - 最多 3 次恢复

4. **会话锁**：per-session mutex 确保同一会话串行处理

### 3.4 AgentRunner

**文件：** `internal/agent/runner.go`

对 Eino Runner 的薄封装，处理 empty/length recovery 逻辑：

```go
type AgentRunner struct {
    runner  *adk.Runner
    cfg     *config.Config
    hooks   []AgentHook
}

func (r *AgentRunner) Run(ctx context.Context, messages []*schema.Message, opts ...adk.AgentRunOption) (*AgentResult, error)
```

封装逻辑：
1. 调用 Eino Runner.Run() 获取事件流
2. 遍历事件流，检测空响应 → 重试（最多 2 次）
3. 检测 finish_reason=length → 追加恢复消息 → 重新调用（最多 3 次）
4. 收集 tool calls、usage、reasoning、最终 content

### 3.5 MemoryStore + Consolidator

**文件：** `internal/agent/memory.go`

```
workspace/memory/
├── MEMORY.md          # 长期记忆（Agent 可编辑）
├── history.jsonl      # 事件历史（追加写）
├── .cursor            # 当前写位置
└── .dream_cursor      # Dream 处理位置
```

**核心操作：**
- `AppendHistory(entry, maxChars)` → cursor 自增 → 时间戳 → JSONL 追加
- `ReadMemory()` / `WriteMemory(content)` → MEMORY.md 读写
- `CompactHistory()` → 超过 1000 条时保留最近 N 条
- `ReadUnprocessedHistory()` → 从 dream_cursor 读取未处理的事件
- 原子写入：tmp + fsync + os.Rename

**Consolidator（记忆合并器）：**
- Token 预算管理：当会话 token 超过阈值触发合并
- LLM 摘要压缩 → 旧消息替换为摘要
- 回退机制：LLM 合并失败 → raw_archive（直接截断保留最近消息）

### 3.6 Dream 两阶段记忆处理

**文件：** `internal/agent/dream.go`

```
Phase 1 (LLM 分析):
  未处理事件 → LLM 分析 → 结构化观察报告
  - 识别重要信息、决策、learnings
  - 建议 MEMORY.md 更新

Phase 2 (AgentRunner 文件编辑):
  观察报告 → AgentRunner 执行 MEMORY.md 编辑
  - 使用 ReadFile + EditFile 工具
  - 行龄标注（go-git blame 获取每行最后修改时间）
```

**Dream 配置：**
- `intervalH`: 2（每 2 小时触发一次）
- `cron`: 可选 cron 表达式覆盖
- `maxBatchSize`: 20（每次最多处理 20 条事件）
- `maxIterations`: 15（Phase 2 最大工具迭代次数）
- `annotateLineAges`: true（标注 MEMORY.md 每行年龄）

### 3.7 ContextBuilder

**文件：** `internal/agent/context.go`

为每次 Agent 调用构建系统提示词：

**Section 结构（8 个 section）：**
1. **Identity** — identity.md 模板渲染
2. **Bootstrap** — AGENTS.md、SOUL.md、USER.md
3. **Tool Contract** — tool_contract.md
4. **Memory** — MEMORY.md 内容（非空时）
5. **Skills** — always-active 技能内容
6. **Skills Summary** — 技能列表摘要
7. **History** — 最近会话历史摘要
8. **Session Summary** — 当前会话状态

**Prompt Cache 优化：**
- 系统提示词前缀保持稳定（不变的部分缓存）
- 运行时上下文（时间、channel、chat_id、用户消息）追加到用户内容末尾

### 3.8 SkillsLoader

**文件：** `internal/agent/skills.go`

- 双层回退：`workspace/skills/<name>/SKILL.md` → `templates/skills/<name>/SKILL.md`
- YAML frontmatter 解析（name、description、always、requires 等）
- `LoadSkillsForContext(names)` → 批量加载，剥离 frontmatter
- `GetAlwaysSkills()` → 筛选 `always: true` 且依赖满足的技能

### 3.9 AgentHook 生命周期

**文件：** `internal/agent/hook.go`

```go
type AgentHook interface {
    BeforeIteration(ctx context.Context, state *IterationState) error
    AfterIteration(ctx context.Context, state *IterationState) error
    OnStream(ctx context.Context, delta string) error
    OnStreamEnd(ctx context.Context, resuming bool) error
    BeforeExecuteTools(ctx context.Context, calls []ToolCallRequest) error
    EmitReasoning(ctx context.Context, delta string) error
    EmitReasoningEnd(ctx context.Context) error
    FinalizeContent(ctx context.Context, content string) (string, error)
}

// CompositeHook 扇出：所有 hook 并行执行，错误隔离
type CompositeHook struct { hooks []AgentHook }
```

### 3.10 SubAgent（子 Agent）

通过 Eino 内置的 `OnSetSubAgents` 接口：

```go
// 创建子 Agent
subAgent := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
    Name:        "code-explorer",
    Description: "Explores and analyzes code",
    Model:       provider,
    ToolsConfig: adk.ToolsConfig{Tools: explorerTools},
})

// 父 Agent 自动获得 transfer_to_agent 工具
parentAgent.OnSetSubAgents(ctx, []adk.Agent{subAgent})
```

Eino 的 `flowAgent` 自动处理 transfer 路由。子 Agent 的 events 可透明转发到父 Agent 的事件流。

支持 `NewAgentTool()` 将子 Agent 包装为工具，提供更灵活的调用方式。

### 验证
- ReAct Agent 端到端测试（mock ChatModel + 真实 tools）
- Governance 中间件测试（orphan/backfill/microcompact/snip 各场景）
- 工具安全中间件测试（SSRF 拦截、workspace 违规、重复查找）
- Empty response recovery 测试
- Length truncation recovery 测试
- Injection handling 测试（中轮消息注入）
- Session load/save 回环测试
- Dream 两阶段集成测试
- ContextBuilder 模板渲染测试
- 并发 session 隔离测试

---

## Phase 4：Channel + 子系统

**依赖：** Phase 3 | **预估：** 8-12 天
**目标：** 飞书消息收发 + WebSocket 前端对接 + Cron + Heartbeat + Command + HTTP API

### 4.1 Channel 接口

**文件：** `internal/channel/base.go`

```go
type Channel interface {
    Name() string
    Start(ctx context.Context, bus *MessageBus) error
    Stop(ctx context.Context) error
    Send(ctx context.Context, msg *OutboundMessage) error
    SupportsStreaming() bool
}
```

### 4.2 Channel 生命周期管理

**文件：** `internal/channel/manager.go`

- `Register(ch Channel)` — 注册 channel
- `StartAll(ctx, bus)` — 启动所有已注册 channel
- `StopAll(ctx)` — 优雅停止
- `Get(name)` — 按名查找
- 外发去重：SHA-1 哈希防止重复发送

### 4.3 飞书 Channel

**文件：** `internal/channel/feishu.go`

**接收消息：**
1. 飞书开放平台事件订阅（Event Subscription）
2. Webhook 回调模式（飞书服务器 → 我们的 HTTP endpoint）
3. 解析飞书消息格式 → `InboundMessage` → 发布到 MessageBus.inbound

**发送消息：**
1. 消费 MessageBus.outbound → 调用飞书发送消息 API
2. 支持文本、Markdown、图片
3. 支持回复模式（`reply_in_thread`）

**认证：**
- `tenant_access_token`（app_id + app_secret）
- Token 缓存 + 自动刷新（过期前 2 小时）

**飞书消息字段映射：**

| 飞书字段 | InboundMessage 字段 |
|---------|-------------------|
| event.sender.open_id | SenderID |
| event.message.chat_id | ChatID |
| event.message.content (JSON 文本字段) | Content |
| event.message.create_time | Timestamp |
| event.message.message_id | Metadata["message_id"] |

### 4.4 WebSocket Channel

**文件：** `internal/channel/websocket.go`

- gorilla/websocket Upgrader（HTTP → WebSocket）
- 多路复用协议：agent 消息、系统事件、心跳
- 与现有 WebUI 前端协议完全兼容

### 4.5 CronService

**文件：** `internal/cron/service.go`

- robfig/cron/v3 后端
- 5 字段 cron 表达式
- 两种任务类型：
  - `durable`：持久化到文件，重启后恢复
  - `session-only`：仅当前会话有效
- 文件持久化格式：`~/.nanobot/cron/jobs.json`

### 4.6 HeartbeatService

**文件：** `internal/heartbeat/service.go`

- 定时唤醒 Agent 检查待处理任务
- `intervalS`：唤醒间隔（默认 1800s）
- `keepRecentMessages`：保留最近 N 条消息在上下文中
- 两阶段 LLM 决策：
  1. 分析当前状态，判断是否需要行动
  2. 如需行动，执行具体任务

### 4.7 CommandRouter

**文件：** `internal/command/router.go`

三级路由：
1. **Builtin 命令**（最高优先级）：`/new`、`/stop`、`/status`、`/skills` 等
2. **Plugin 命令**：插件注册的自定义命令
3. **动态路由**：通过 LLM 理解的 fallback

内置命令列表（12 个）：
- `/new` — 新建会话
- `/stop` — 停止当前处理
- `/status` — 显示运行状态
- `/skills` — 列出可用技能
- `/channels status` — Channel 状态
- `/channels login [name]` — 登录 channel
- `/plugins list` — 插件列表
- `/provider login [name]` — 配置 provider
- `/provider logout [name]` — 移除 provider
- `/help` — 帮助

### 4.8 API Server

**文件：** `internal/api/server.go`

OpenAI 兼容 HTTP API：
- `POST /v1/chat/completions` — 支持 streaming 和非 streaming
- `GET /v1/models` — 可用模型列表
- `GET /health` — 健康检查
- 内部流程：HTTP 请求 → InboundMessage → AgentLoop → OpenAI 格式响应

### 验证
- 飞书消息收发完整流程测试（飞书开放平台沙箱环境）
- WebSocket 协议与 WebUI 前端兼容性测试
- OpenAI SDK 兼容性测试（Python openai 库可直接对接）
- Cron 任务创建/执行/持久化测试
- Heartbeat 两阶段决策测试
- 所有 12 个内置命令集成测试

---

## Phase 5：CLI + 打磨 ⏳

**依赖：** Phase 4 | **预估：** 3-5 天 | **状态：** gateway 优先，agent 搁置

### 5.1 CLI 架构

基于 cobra + viper，参考 `nanobot-go/cmd/nanobot/` 的命令设计模式。

**入口文件：** `cmd/nanobot/main.go`

```
nanobot                                     # 根命令
  --config <path>                           # 配置文件路径（持久标志，默认 ~/.nanobot/config.json）
  |
  +-- version                               # 打印版本信息（Phase 0 可用）
  |
  +-- onboard                               # 初始化配置和工作区（Phase 0 可用）
  |
  +-- status                                # 显示当前配置和状态（Phase 0 可用）
  |
  +-- agent                                 # 交互式聊天/单次消息（搁置，优先 gateway）
  |     -m, --message <text>                #   发送单条消息后退出（基础可用）
  |     --raw                               #   纯文本输出，不使用 markdown 渲染
  |
  +-- gateway                               # 启动完整网关服务器（优先实现 ✅）
```

**根命令设计（main.go）：**
- `PersistentPreRunE` 钩子：`onboard`/`version`/`help` 命令跳过配置加载，其余命令自动调用 `mustLoadConfig()`
- viper 绑定 `--config` 持久标志到 `cfgFile` 变量
- 注册 5 个子命令：`version`、`onboard`、`status`、`agent`、`gateway`

**公共辅助函数：**
- `mustLoadConfig()` — 调用 `config.Load(cfgFile)`，失败时 `os.Exit(1)`
- `initLogging()` — 初始化 slog JSON handler
- `maskSecret(s)` — API key 脱敏（前4+`****`+后4）

### 5.2 各命令实现状态

| 命令 | 状态 | 说明 |
|------|------|------|
| `version` | ✅ | 打印 `Version` 变量（ldflags 注入，默认 `"dev"`） |
| `onboard` | ✅ | 创建 `~/.nanobot/` 目录树 + 默认 `config.json` |
| `status` | ✅ | 打印 model/agent/channels/gateway/api/providers/presets/mcp/paths 全部配置 |
| `agent` | ⏸️ 搁置 | 基础单消息模式可用，完整 REPL（liner + glamour + spinner）等 gateway 稳定后再做 |
| `gateway` | ✅ 优先 | 10步集成启动（bus→chatModel→tools→agent→loop→channels→cron→heartbeat→api→commands），优雅关闭 |

**onboard 命令（`cmd/nanobot/onboard.go`）：**
1. 创建 `~/.nanobot/` 配置目录（0755）
2. 若 `config.json` 不存在，写入默认配置
3. 创建 7 个子目录：`prompts/`、`skills/`、`workspace/`、`sessions/`、`memory/`、`cron/`、`logs/`
4. 打印下一步提示

**status 命令（`cmd/nanobot/status.go`）：**
- 读取配置后分 7 个区段打印：
  - **Model**：model/provider/max_tokens/temperature/reasoning_effort/timezone
  - **Agent**：workspace/context_window/max_iterations/max_subagents/retry 等
  - **Channels**：send_progress/show_reasoning/transcription
  - **Gateway**：host/port/heartbeat
  - **API**：host/port/timeout
  - **Providers**：4 个 provider 的配置状态（已配置/未配置）
  - **Model Presets** / **MCP Servers**：如有配置则列出
  - **Paths**：config_dir/config_file/workspace

**agent 命令（`cmd/nanobot/agent.go`，Phase 4+ 完整实现）：**
- 交互式 REPL：`peterh/liner` 库（历史记录 `~/.nanobot/cli_history`）
- 单次消息模式：`-m` flag → 发送 → 收集完整响应 → 打印 → 退出
- Markdown 渲染：`charmbracelet/glamour`（`--raw` 跳过）
- Thinking spinner：goroutine 驱动的旋转动画（`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`）
- 流式消费：`bot.ChatStream()` → 收集所有 content delta → 拼接完整响应
- Ctrl-C 中断支持

**gateway 命令（`cmd/nanobot/gateway.go`，Phase 4+ 完整实现）：**
- 完整网关启动流程：
  1. 初始化 OpenTelemetry tracing
  2. 同步 prompt/skills 模板到 workspace
  3. 初始化 MemoryStore + SessionManager
  4. 创建 MessageBus（inbound/outbound channels）
  5. 构建 ChatModel（从配置 + registry + factory）
  6. 启动 CronService（cron job handler 构建）
  7. 启动 HeartbeatService（定时 LLM 健康检查）
  8. 构建 ToolConfig + SubagentManager
  9. 创建 Agent（Eino ChatModelAgent + 中间件 + 子 Agent）
  10. 启动飞书 Channel（如已配置）
  11. 启动入站消息循环（`RunInboundLoop`）
  12. 信号处理（SIGINT/SIGTERM → 15s 优雅关闭 → 组件 5s 超时停止）
- 参考：`nanobot-go/cmd/nanobot/gateway.go` 的 `runGateway()` 实现

### 5.2 安全

**文件：** `internal/security/ssrf.go`

- SSRF 检测：检查 URL 是否指向内部/私有地址
- Workspace 路径安全：文件操作必须在工作区内
- 与 Phase 3 的工具安全中间件集成

### 5.3 工具函数

**文件：** `internal/utils/`（按需创建）

- `strip_think()` — Think 标签剥离（从 thinking 中间件提取通用逻辑）
- `helpers.go` — 通用工具函数

### 5.4 公共 SDK Facade

**文件：** `pkg/nanobot.go`

```go
type Nanobot struct { ... }

func New(cfg config.Config) (*Nanobot, error)
func (n *Nanobot) Start(ctx context.Context) error
func (n *Nanobot) Stop(ctx context.Context) error
func (n *Nanobot) SendMessage(ctx context.Context, msg *InboundMessage) (*AgentResult, error)
```

管理 AgentLoop、Channel、API Server 的生命周期和优雅关闭。

### 5.5 集成测试

- 完整消息流：飞书 webhook → AgentLoop → Eino ReAct Agent → 飞书发送
- Mock 飞书服务器 + Mock LLM 响应（go-vcr）
- 多 session 并发测试
- Checkpoint 中断/恢复测试

### 5.6 文档

- Go doc 注释覆盖率
- README.md（快速开始 + 飞书配置指南）
- 飞书开放平台配置指南

### 验证
- 跨平台二进制构建（linux/darwin/windows）
- E2E 完整流程通过
- `go doc` 可生成文档
- 性能基准测试（token 估计、session save 延迟、并发吞吐量）

---

## 核心 Go 接口映射

| Python ABC | Go Interface | Package |
|---|------|------|
| `LLMProvider` | `ChatModelAdapter` (包装 `model.BaseChatModel`) | `internal/provider` |
| `Tool` | `Tool` | `internal/tool` |
| `Schema` | `SchemaElement` | `internal/tool` |
| `SandboxBackend` | `SandboxBackend` | `internal/tool` |
| `AgentHook` | `AgentHook` | `internal/agent` |
| `BaseChannel` | `Channel` | `internal/channel` |
| `MessageBus` | `MessageBus` (struct) | `internal/bus` |
| `SessionManager` | `SessionManager` (struct) | `internal/session` |

---

## 测试策略

| 层次 | 方法 | 说明 |
|------|------|------|
| 单元测试 | table-driven，mock 外部依赖 | 每个导出函数 |
| 集成测试 | go-vcr 录制 HTTP 响应，temp 目录 | Provider 调用、文件操作 |
| 中间件测试 | 构造消息列表，验证中间件处理结果 | Governance、安全、thinking 中间件 |
| E2E 测试 | 启动完整 gateway，模拟消息收发 | 飞书沙箱 + WebUI 连接 |

## 每个 Phase 的验证检查清单

1. `go build ./...` 通过（零编译错误）
2. `go vet ./...` 零警告
3. `go test ./...` 通过（含 race detector：`go test -race ./...`）
4. Phase 3+：发送测试消息 → 验证完整回复链路

---

## 与 Python 版的关键差异总结

| 方面 | Python 版 | Go/Eino 版 | 说明 |
|------|----------|-----------|------|
| ReAct 循环 | 手写 AgentRunner（~500 行） | Eino ChatModelAgent（内置 Graph） | Eino 负责 think→act→observe 循环，我们负责 governance 中间件 |
| Governance | 手写（5 步消息修复） | Governance 中间件（BeforeModelRewriteState） | 同等功能，通过 Eino 中间件扩展实现 |
| Provider 数量 | ~50 个注册项 | 4 个（可扩展） | registry + factory 保留，backend 类型驱动 |
| Provider 匹配 | 5 级优先级自动匹配 | Registry.Match(model) | 保留自动匹配，范围缩小 |
| Channel | 15+ 平台 | 飞书 + WebSocket | 其余平台后续按需添加 |
| Dream | 两阶段 LLM 记忆处理 | 保留 | 两阶段：LLM 分析 → AgentRunner 文件编辑 |
| MCP 工具 | 支持 | 保留 | mark3labs/mcp-go |
| 子 Agent | SubagentManager | Eino 内置（transfer_to_agent + flowAgent） | 使用框架能力，更简洁 |
| 中间件 | 无框架中间件概念 | 3 个自定义 ChatModelAgentMiddleware | Governance + ToolSafety + Thinking |
| 检查点 | 3 阶段手动检查点 | Eino CheckPointStore 自动管理 | 通过 Runner 管理 |
| Consolidator | LLM 摘要 | 保留 | Token 预算 → 摘要 → raw_archive 回退 |
| AutoCompact | TTL 空闲压缩 | 保留 | 通过 CronService 定时触发 |
| 图像生成/语音转写 | 支持 | 暂不实现 | 非核心功能，后续按需 |

---

## 总时间预估

| 阶段 | 预估 | 状态 |
|------|------|------|
| Phase 0: 基础设施 | 2-3 天 | ✅ 完成 |
| Phase 1: Provider 子系统 | 7-10 天 | ✅ 完成 |
| Phase 2: Tool 子系统 | 5-7 天 | ✅ 完成 |
| Phase 3: Agent Core | 7-10 天 | ✅ 完成 |
| Phase 4: Channel + 子系统 | 8-12 天 | ✅ 完成 |
| Phase 5: CLI + 打磨 | 3-5 天 | ⏳ gateway 可用，agent 基础可用，API streaming 搁置 |
| **合计** | **32-47 天** | |

相比纯手写方案（48-69 天）节省约 30-35%，主要节省来自：
- Eino ChatModelAgent 承担 ReAct 循环（省去手写状态机、分支路由、流式管道）
- Eino 提供 CheckPoint/Resume（省去手写序列化/恢复逻辑）
- Eino 提供子 Agent 和 transfer 路由（省去手写 SubagentManager）
- Eino 提供 Workflow 编排（Sequential/Parallel/Loop Agent）
