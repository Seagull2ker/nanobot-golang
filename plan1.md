# plan1.md — nanobot-golang 相比 nanobot-go 的遗漏功能

对比基准：`D:\projects\nanobot-go`（83 个 Go 文件，18 个 pkg 包）

---

## 一、会话管理（Session Manager）

**来源：** `pkg/session/manager.go`

**现状：** nanobot-golang 在 `internal/types/sessions.go` 只定义了 Session 类型（14行），无持久化。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| JSONL 持久化 | Session 存为 JSONL 文件（metadata header + 每行一条 message JSON） |
| 缓存+磁盘 | 内存缓存 + 磁盘加载，`Invalidate()` 使缓存失效 |
| LastConsolidated 索引 | 跟踪哪些消息已归档到长期记忆，供 MemoryConsolidator 使用 |
| user-turn 对齐 | `GetHistory()` 确保返回的消息始于 user role（避免 orphan tool_result） |
| 大消息缓冲 | 1MB buffer 处理长 tool output |
| 会话列表 | `ListSessions()` 列出磁盘上所有会话 |

**优先级：** 高 — agent 需要跨轮次记忆

---

## 二、记忆系统（Memory Consolidation）

**来源：** `pkg/memory/memory.go` + `consolidator.go`

**现状：** 我们有 `internal/agent/memory.go`（MEMORY.md 读写 + history.jsonl 追加），但缺少 LLM 驱动的合并。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| LLM-based consolidation | 调用 LLM 用 `save_memory` tool 将对话摘要写入 MEMORY.md |
| 两层记忆 | MEMORY.md（覆盖式长期事实）+ HISTORY.md（追加式带时间戳日志） |
| Token 预算触发 | `MaybeConsolidateByTokens()` — 超出 contextWindow/2 时触发 |
| 归档边界选择 | `PickConsolidationBoundary()` — 在 user-turn 边界处安全截断 |
| 原始归档回退 | 连续 3 次 LLM 合并失败后改为 raw archive（不丢消息） |
| 合并轮数上限 | `maxConsolidationRounds=5` |
| `/new` 命令前存档 | `ArchiveUnconsolidated()` 清空前持久化 |

**优先级：** 高 — 长对话的上下文管理核心

---

## 三、子 Agent 系统（Subagent）

**来源：** `pkg/subagent/manager.go`

**现状：** 完全没有。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| SubagentManager | 后台 spawn 子 agent 独立执行任务 |
| 受限工具集 | 子 agent 只能访问 filesystem/shell/web 工具（不可 spawn/cron/MCP） |
| 总线通知 | 子 agent 完成后通过 MessageBus 发布 `subagent_result` 消息 |
| 每会话取消 | `CancelBySession()` — `/stop` 时取消该会话的所有子 agent |
| 全局取消 | `CancelAll()` — 关闭时取消所有 |
| 原子任务 ID | `atomic.Int64` 生成唯一 task ID |
| 最大步数限制 | 默认 15 步，带用户友好的超限提示 |

**优先级：** 高 — plan.md Phase 3 明确要求 SubAgent

---

## 四、Tool Wrapper（工具结果包装器）

**来源：** `pkg/tools/wrapper.go`

**现状：** 没有统一的工具结果规范化层。plan.md 2.8 节已规划但未实现。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| 结果截断 | `ToolResultMaxChars=16000`，超长从中间截断 |
| 错误降级 | 工具返回 error → 转为 `"Error executing {name}: {err}"` 字符串（不中断 agent） |
| 失败提示 | Error 结果末尾追加 `[Analyze the error above and try a different approach.]` |
| 进度回调 | `ToolProgressFunc(ctx, toolName, status)` — running/completed/failed |
| 装饰器模式 | `WrapTools(tools, maxChars, onProgress)` 不修改原工具 |

**优先级：** 高 — plan.md 已规划，影响 agent 稳定性

---

## 五、TurnContext（轮次上下文）

**来源：** `pkg/tools/turnctx.go`

**现状：** 没有。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| 消息已发标记 | `atomic.Bool` 跟踪当前轮次是否已发消息，避免重复发送 |
| 会话 ID 传递 | `ContextWithSessionID/FromContext` — context 中传递 session key |
| 进度信息传递 | `ProgressInfo{Channel, ChatID}` 通过 context 传递给 tool |
| 输入角色标记 | `ContextWithInputRole(ctx, "assistant")` 标记子 agent 输入 |

**优先级：** 中 — agent loop 需要这些信息来正确处理消息

---

## 六、Message Tool（消息工具）

**来源：** `pkg/tools/message.go`

**现状：** 没有。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| SendMessage 工具 | agent 可通过工具调用主动向用户发送消息 |
| 回调解耦 | `SendMessageFunc` 回调模式，不直接依赖总线 |
| 默认 channel/chatID | 未指定时从 context 获取 |
| 媒体附件 | 支持 Media 字段 |

**优先级：** 中

---

## 七、Cron Tool（定时任务工具接口）

**来源：** `pkg/tools/cron.go`

**现状：** 我们有 `internal/cron/service.go`（186行），但没有对应的 tool 接口让 agent 管理 cron 任务。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| add/list/remove 操作 | 工具接口供 agent 调用 |
| CronSchedule | every/cron/at 三种模式 |
| 重复调度上下文 | `WithCronContext` 防止 cron 触发时再次调度 cron |

**优先级：** 中 — 服务已有，缺少 tool 包装

---

## 八、MCP 集成（Model Context Protocol）

**来源：** `pkg/tools/mcp.go`

**现状：** 没有。plan.md Phase 2 优先级 2 已规划。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| 连接管理 | `ConnectMCPServer()` — 支持 stdio/SSE/streamableHttp 三种传输 |
| 工具发现 | 查询 MCP server 的 tool list，包装为 `mcpToolWrapper` |
| 惰性初始化 | 首次消息时才连接 MCP server |
| Agent 重建 | MCP 工具连接后重建 react agent |
| 超时控制 | 每个 MCP 工具默认 30s 超时 |
| 头注入 | `headerTransport` 自定义 HTTP headers |

**优先级：** 中 — plan.md 已规划

---

## 九、Spawn Tool（子 Agent 生成工具）

**来源：** `pkg/tools/spawn.go`

**现状：** 没有。依赖 SubagentManager。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| `spawn` tool | agent 通过工具调用启动后台子任务 |
| 回退 channel/chatID | 从 ProgressInfo context 解析 |

**优先级：** 中 — 依赖 subagent 系统

---

## 十、Skills 系统完善

**来源：** `pkg/skill/manager.go`（426行）

**现状：** 我们有 `internal/agent/skills.go`（144行），但功能简化很多。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| YAML frontmatter + JSON metadata | 双层解析：frontmatter 基础信息 + metadata 中 JSON 扩展（requires/install） |
| 依赖检查 | `isAvailable()` — `exec.LookPath` 检查 required_bins + `os.Getenv` 检查 required_env |
| 安装提示 | `getInstallInstructions()` — 生成 `brew install` / `apt install` 命令 |
| XML 技能摘要 | `<skills>` block 注入 system prompt，含可用性/描述/路径/安装提示 |
| workspace-over-builtin | workspace 同名 skill 覆盖 builtin |
| NanobotMeta | emoji/os/requires/install 扩展字段 |

**优先级：** 低 — 当前足够用

---

## 十一、内存系统完善

**来源：** `pkg/memory/memory.go`（258行）

**现状：** 我们的 `internal/agent/memory.go`（91行）缺少以下能力：

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| LLM 驱动的 `Consolidate()` | 使用 forced `save_memory` tool 生成摘要 |
| 两层存储 | HISTORY.md（带时间戳的 grep 友好日志） |
| 归一化时间戳 | `[YYYY-MM-DD HH:MM]` 前缀确保可搜索 |
| `SaveMemoryArgs` | 结构化的记忆更新参数 |

---

## 十二、Prompt Loader

**来源：** `pkg/prompt/loader.go`（63行）

**现状：** 系统提示在 `internal/agent/context.go` 和 `onboard.go` 中内联处理。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| 多文件组合 | SOUL.md/USER.md/TOOLS.md/AGENTS.md/HEARTBEAT.md → 一个 system message |
| 必须文件检查 | 除 HEARTBEAT.md 外必须存在 |
| 节标题 | `# SOUL` / `# USER PROFILE` / `# TOOL USAGE` 等结构化标签 |

---

## 十三、Workspace 模板同步

**来源：** `pkg/workspace/sync.go`（54行）

**现状：** 我们的 `onboard.go` 内联了模板写入。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| Go embed | 模板文件嵌入 binary（`//go:embed templates/*.md`） |
| Skip-if-exists | 已有文件绝不覆盖 |
| 独立包 | workspace 包可用于 onboard/agent/gateway 三种场景 |

---

## 十四、RunInboundLoop 增强

**来源：** `pkg/app/runloop.go`

**现状：** 我们的 `internal/agent/loop.go`（69行）只有一个基础骨架。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| 每会话 goroutine 隔离 | `sync.Map` 维护 per-session worker，串行执行同会话，并发跨会话 |
| WaitGroup 关闭 | graceful shutdown 等待所有 worker 排空 |
| 重试瞬时错误 | `apperr.Retryable(err)` → 2s 后重试一次 |
| 空回复启发式 | 如果 stream 失败且未产出内容且未发消息 → 发送 public error |
| 系统路由解码 | `DecodeSystemRoute` 将 `system:<channel>:<chatID>` 解码为真实路由 |
| Langfuse trace | 每个消息自动创建 Langfuse trace |
| public message 回退 | 最终错误通过 `apperr.PublicMessage(err)` 转化为对外消息 |

---

## 十五、Graceful Shutdown 完善

**来源：** `pkg/app/shutdown.go`

**现状：** 我们的 gateway.go 有简单的 signal→cancel→stop 流程。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| RuntimeComponents 结构体 | 统一收集需关闭的组件 |
| 多阶段关闭 | 取消 root → 取消 tasks → 关闭 inbound → drain WaitGroup → 停止组件 |
| CancelAll agent + subagent | 日志记录取消的任务数 |
| 超时安全网 | `ShutdownTimeout` + `time.After` |
| 信号通道创建 | `NewSignalChannel()` 封装 SIGINT/SIGTERM |

---

## 十六、Provider Registry 扩展

**来源：** `pkg/config/providers.go`

**现状：** 我们有 4 个 provider。nanobot-go 有 16 个。

**遗漏的 provider：**

| Provider | Eino 适配 |
|----------|----------|
| OpenRouter | openrouter |
| AiHubMix | openai_compat |
| SiliconFlow | openai_compat |
| Ollama | ollama |
| Gemini | gemini |
| Groq | openai_compat |
| Zhipu (GLM) | openai_compat |
| Moonshot (Kimi) | openai_compat |
| MiniMax | openai_compat |
| Mistral | openai_compat |
| Volcengine Ark (Doubao) | ark |

**遗漏的匹配策略：**
- `DetectByBase` — 通过 api_base URL 特征匹配 provider
- 多策略优先级：显式名称 → 模型前缀 → 关键词 → api_base → 首个有 key 的 provider

---

## 十七、Config 完善

**来源：** `pkg/config/schema.go`

**遗漏字段：**

| 结构体 | 遗漏项 |
|--------|--------|
| AgentConfig | `PromptDir`, `BuiltinSkillsDir`, `ContextWindowTokens`, `MaxStep`, `MaxTokens`, `Temperature`, `ReasoningEffort`, `Provider`, `Model` |
| ProviderConfig | `APISecret`（部分 provider 需要） |
| ToolsConfig | `Exec` (ExecConfig 含 Timeout/MaxOutput/DenyPatterns/AllowPatterns), `Web` (WebConfig), `MCP []MCPConfig`, `RestrictToWorkspace`, `ExtraReadDirs` |
| ExecConfig | `Timeout`, `MaxOutput`, `DenyPatterns`, `AllowPatterns`, `PathAppend` |
| WebConfig | `Search` (含 Provider/APIKey/BaseURL/MaxResults/Proxy) |
| MCPConfig | `Type` (stdio/sse/streamableHttp), `Command`, `Args`, `Env`, `URL`, `Headers`, `ToolTimeout`, `EnabledTools` |
| TracingConfig | `Enabled`, `Endpoint`, `PublicKey`, `SecretKey` |
| Duration 类型 | JSON 接受 `"30m"` 字符串或数字秒数 |

---

## 十八、Tracing（可观测性）

**来源：** `pkg/trace/trace.go` + `span.go`

**现状：** 没有。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| Langfuse 初始化 | `trace.Init()` 全局注册 Eino 回调 |
| 手动 Span | `StartSpan/EndSpan` 为非 Eino 组件（如 memory consolidation）创建 trace |
| 运行信息注入 | `RunInfo.Component` 识别组件类型 |

---

## 十九、CLI 交互式 REPL

**来源：** `cmd/cli/main.go`

**现状：** 我们的 agent.go 只有一个简单的 stdin 循环。

**遗漏内容：**

| 功能 | 说明 |
|------|------|
| liner 历史 | `peterh/liner` — 上下箭头历史记录 |
| glamour 渲染 | `charmbracelet/glamour` — 终端 Markdown 渲染 |
| spinner | 思考中旋转动画 |
| `/stop` 命令 | `os.FindProcess` + `syscall.Kill` 向 gateway 发 SIGTERM |
| 流式输出 | 逐 token 打印 |
| 配置热加载 | 从 Viper 重新读取配置 |

---

## 二十、/stop, /restart 命令

**来源：** `pkg/agent/agent.go`

**遗漏内容：**

| 命令 | 功能 |
|------|------|
| `/stop` | 取消当前 session context，级联取消子 agent |
| `/restart` | `syscall.Exec` 原地进程替换，重连飞书 WS |
| `/help` | 列出内置命令 |

---

## 二十一、其他细节

| 项目 | 说明 |
|------|------|
| `apperr` 包 | nanobot-go 有独立的错误分类包（11 种 Kind），含 `PublicMessage()` 安全化 |
| `reactutil` 包 | 避免 agent/subagent 循环引用的共享 react 工具 |
| `model` 包 | LLM 工厂，自动适配 ChatModel / ToolCallingChatModel |
| ExecSessionManager | 长运行 shell 会话管理（write_stdin / list_exec_sessions）— nanobot-go 也未实现 |
| Multi-instance | `SetConfigPath()` 允许不同实例使用不同数据目录 |

---

---

## 补充：Workspace 内容详细对比

### 一、Prompt 模板文件

**nanobot-go** 有 5 个完整的 `.md` 文件通过 `//go:embed` 嵌入 binary：

| 文件 | 大小 | 内容 |
|------|------|------|
| `SOUL.md` | 353B | 人格定义："I am nanobot, a personal AI assistant"，Personality（性格特征），Values（价值观），Communication Style（交流风格） |
| `USER.md` | 842B | 用户档案：基本信息（姓名/时区/语言占位）、偏好（沟通风格/回答长度/技术水平）、工作上下文、兴趣、特殊指示 |
| `TOOLS.md` | 487B | 工具使用说明：exec 安全限制（deny patterns/超时/截断）、cron 提醒用法（引用 cron skill） |
| `AGENTS.md` | 1144B | Agent 指令：用户档案管理（检测个人信息→edit_file 更新 USER.md）、定时提醒（用 cron tool 而非 exec）、Heartbeat 任务管理 |
| `HEARTBEAT.md` | 365B | 心跳任务清单（Active Tasks / Completed 两个区段，HTML 注释占位） |

**我们** 的 `onboard.go` 中 `syncTemplates()` 只写入一行占位内容：
```go
"SOUL.md":   "# Soul\n\nYou are nanobot, a helpful AI assistant.\n"
"USER.md":   "# User\n\nUser profile and preferences.\n"
```
→ **全部需要替换为 nanobot-go 的完整内容**

### 二、Go embed 模板嵌入

**nanobot-go** 使用 Go 1.16+ 的 `embed` 包：
```go
//go:embed templates/*.md
var embeddedTemplates embed.FS
```
`SyncTemplates()` 从 binary 读取 → 写入目标目录，已有文件**绝不覆盖**。

**我们** 在 `onboard.go` 中用 `map[string]string` 内联写入。

→ **需要：** 创建 `internal/workspace/` 包（或放到现有包），用 `embed` 嵌入模板文件，`SyncTemplates()` 支持 skip-if-exists

### 三、Prompt Loader

**nanobot-go** 有 `pkg/prompt/loader.go`（63行）：
```
BuildSystemMessages() → 读取5个文件 → 组装为带节标题的系统消息
  # SOUL
  (SOUL.md 内容)
  # USER PROFILE
  (USER.md 内容)
  # TOOL USAGE
  (TOOLS.md 内容)
  # AGENT INSTRUCTIONS
  (AGENTS.md 内容)
  # HEARTBEAT TASKS  ← 可选
  (HEARTBEAT.md 内容)
```
4 个核心文件（SOUL/USER/TOOLS/AGENTS）必须存在，HEARTBEAT 可选。

**我们** 的 `internal/agent/context.go` 的 `BuildSystemPrompt()` 只有：
```
identity → bootstrap → memory → skills summary
```
没有从文件加载 prompt 的逻辑。

→ **需要：** 将 `ContextBuilder` 改为从 prompt 目录读取文件组装

### 四、内置 Skills（SKILL.md）

**nanobot-go** 在 `configs/skills/` 下有 8 个内置技能：

| 技能 | 说明 |
|------|------|
| `clawhub` | 第三方技能仓库 |
| `cron` | 定时提醒和周期性任务 |
| `github` | 用 `gh` CLI 操作 GitHub |
| `memory` | 两层记忆系统（MEMORY.md + HISTORY.md grep） |
| `skill-creator` | 创建新技能的模板 |
| `summarize` | 摘要/提取 URL/播客/文件内容 |
| `tmux` | tmux 终端复用 |
| `weather` | 天气查询（curl wttr.in，无需 API key） |

每个 SKILL.md 格式：
```markdown
---
name: cron
description: Schedule reminders and recurring tasks
metadata: {"nanobot":{"emoji":"⏰","requires":{"bins":[]}}}
---

(cron 技能的使用说明正文)
```

**我们** 的 `internal/agent/skills.go` 有解析逻辑，但没有内置 skill 文件来源。

→ **需要：** 创建 `templates/skills/` 目录 + 至少 3-4 个常用 skill 的 SKILL.md

### 五、Workspace 目录树完整对比

| 目录/文件 | nanobot-go | 我们 | 差距 |
|-----------|-----------|------|------|
| `prompts/SOUL.md` | Go embed + 完整内容 | onboard 内联占位 | 需替换内容 + embed |
| `prompts/USER.md` | 842B 结构化档案 | 占位 | 需替换 |
| `prompts/TOOLS.md` | 487B 工具说明 | 占位 | 需替换 |
| `prompts/AGENTS.md` | 1144B agent 指令 | 占位 | 需替换 |
| `prompts/HEARTBEAT.md` | 365B 心跳清单 | 占位 | 需替换 |
| `skills/` (builtin) | 8 个 SKILL.md | 空目录 | 需创建 4-5 个 |
| `workspace/` | agent 工作区 | ✅ 已有 | — |
| `sessions/` | JSONL 会话 | ✅ 已有 | — |
| `memory/` | MEMORY.md + HISTORY.md | ✅ 已有 | — |
| `media/` | 媒体文件 | ✅ 已有 | — |
| `cron/` | jobs.json | ✅ 已有 | — |
| `logs/` | 日志 | ✅ 已有 | — |
| `history/` | cli_history | ✅ 已有 | — |

### 六、Workspace 相关代码组织对比

| 功能 | nanobot-go | 我们 | 差距 |
|------|-----------|------|------|
| 模板嵌入 | `pkg/workspace/sync.go` + `templates/` | 无 | 需要创建 |
| Prompt 加载 | `pkg/prompt/loader.go` | `agent/context.go` 内联 | 需抽取或增强 |
| 技能加载 | `pkg/skill/manager.go`（426行） | `agent/skills.go`（144行） | 需增加 XML摘要、依赖检查、安装提示 |
| 前端 YAML 解析 | 逐行 key:value | 逐行 key:value | ✅ 一致 |

---

## 优先级总结

---

## 补充：onboard 生成结果逐项对比

对比 `~/.nanobot-eino/`（nanobot-go）和 `~/.nanobot/`（我们）的实际内容。

### 目录树对比

```
nanobot-go (~/.nanobot-eino/)          我们 (~/.nanobot/)
├── config.yaml (58行, 7段)           ├── config.yaml (基础结构)
├── prompts/                          ├── prompts/
│   ├── SOUL.md       (22行, 353B)    │   ├── SOUL.md       (1行, 占位)
│   ├── USER.md       (49行, 842B)    │   ├── USER.md       (1行, 占位)
│   ├── TOOLS.md      (16行, 487B)    │   ├── TOOLS.md      (1行, 占位)
│   ├── AGENTS.md     (28行, 1144B)   │   ├── AGENTS.md     (1行, 占位)
│   └── HEARTBEAT.md  (15行, 365B)    │   └── HEARTBEAT.md  (1行, 占位)
├── skills/           (空目录)         ├── skills/           (空目录)
├── workspace/        (空目录)         ├── workspace/        (空目录)
├── sessions/                         ├── sessions/         (空目录)
│   └── *.jsonl (运行时生成)           ├── memory/           (空目录)
├── memory/                           ├── media/            (空目录)
│   ├── MEMORY.md     (4行)           ├── cron/             (空目录)
│   └── HISTORY.md    (2行)           ├── logs/             (空目录)
├── cron/             (空目录)         └── history/          (空目录)
├── logs/             (空目录)
└── history/          (空目录)
```

### 逐文件差距

#### 1. config.yaml

| 方面 | nanobot-go | 我们 |
|------|-----------|------|
| **格式** | JSON（虽然扩展名 .yaml） | JSON（扩展名 .yaml） |
| **agent 段** | promptDir, builtinSkillsDir, contextWindowTokens(65536), maxStep(50), maxTokens(8192), temperature(0.1), provider("deepseek"), model("deepseek-v4-flash") | 只有 model/provider/maxTokens/temperature + workspace/botName 等更复杂的嵌套 |
| **providers 段** | `map[string]ProviderConfig`（动态 key） | `ProvidersConfig` struct（40+ 字段，每个 provider 一个固定字段） |
| **channels 段** | feishu (appId/appSecret/allowFrom/verificationToken/encryptKey) | feishu 字段齐全 ✅ |
| **gateway.heartbeat** | `path` + `interval`（字符串 "30m0s" Duration 类型） | `enabled`(bool) + `intervalS`(int) + `keepRecentMessages`(int) |
| **gateway.cron** | `storePath` | 无（cron 路径硬编码在 paths.go） |
| **tools 段** | workspace + restrictToWorkspace + web.search + exec.timeout | 只有 tools + mcpServers |
| **data 段** | dir + memoryDir（可覆盖运行时目录） | 无 — 所有路径硬编码 |
| **trace 段** | enabled + endpoint + publicKey + secretKey | 无 |
| **Duration 类型** | `"30m0s"` 字符串格式 | 无此类型，用 int 秒 |

#### 2. prompts/SOUL.md

| nanobot-go | 我们 |
|-----------|------|
| 22 行完整人格定义：我是 nanobot 🐈 / Personality (3 traits) / Values (3条) / Communication Style (3条) | `# Soul\n\nYou are nanobot, a helpful AI assistant.\n` |

#### 3. prompts/USER.md

| nanobot-go | 我们 |
|-----------|------|
| 49 行结构化档案：Basic Information (姓名/时区/语言) / Preferences (沟通风格/回答长度/技术水平含 checkbox) / Work Context (角色/项目/工具) / Topics of Interest / Special Instructions | `# User\n\nUser profile and preferences.\n` |

#### 4. prompts/AGENTS.md

| nanobot-go | 我们 |
|-----------|------|
| 28 行 Agent 行为指令：用户档案管理（检测个人信息→edit_file USER.md）/ 定时提醒（用 cron tool 非 exec）/ Heartbeat 任务管理（edit_file/write_file 管理 HEARTBEAT.md） | `# Agents\n\nAgent configuration and behavior instructions.\n` |

#### 5. prompts/TOOLS.md

| nanobot-go | 我们 |
|-----------|------|
| 16 行工具约束说明：exec 安全限制（超时/deny patterns/输出截断/workspace限制）/ cron 提醒（引用 cron skill） | `# Tools\n\nYou have access to tools to help the user.\n` |

#### 6. prompts/HEARTBEAT.md

| nanobot-go | 我们 |
|-----------|------|
| 15 行心跳任务模板：Active Tasks + Completed 区段 + HTML注释占位 | `# Heartbeat\n\nPeriodic check-in tasks.\n` |

#### 7. memory/MEMORY.md

| nanobot-go | 我们 |
|-----------|------|
| 初始化为含示例内容的模板：`# Nanobot's Long-term Memory\n\n## User Interactions\n- User greeted...` | 目录存在但文件不创建（运行时才创建） |

#### 8. memory/HISTORY.md

| nanobot-go | 我们 |
|-----------|------|
| 创建时含一条示例历史：`[2026-05-23 22:49] User greeted...` | 不创建 |

#### 9. sessions/*.jsonl

| nanobot-go | 我们 |
|-----------|------|
| 运行时生成：metadata header + 每行 message JSON | 无持久化（P0待实现） |

#### 10. config.yaml 缺失段

我们 Config 结构体中完全没有的段：

| 段 | 用途 | 重要性 |
|----|------|--------|
| `data.dir` | 覆盖 sessions 目录 | 低 |
| `data.memoryDir` | 覆盖 memory 目录 | 低 |
| `tools.web.search` | 搜索 provider 配置 | 中 |
| `tools.exec` | exec 超时/输出上限/deny patterns 可配置 | 高 |
| `tools.restrictToWorkspace` | 文件操作限制 | 中 |
| `gateway.cron.storePath` | cron 持久文件路径 | 低 |
| `trace` | Langfuse 可观测性 | 低 |
| **Duration 类型** | 支持 `"30m"` 字符串格式 | 中 |

### 补充差距总结（Workspace + Config）

| # | 差距 | 涉及文件 | 优先级 |
|---|------|---------|--------|
| W1 | 5 个 prompt 模板全部从占位替换为完整内容 | `onboard.go` `syncTemplates()` | P1 |
| W2 | Go embed 嵌入模板文件（不再内联 map） | 新建 `templates/*.md` + workspace 包 | P2 |
| W3 | Prompt Loader 独立包 | 新建 `internal/prompt/loader.go` | P2 |
| W4 | MEMORY.md + HISTORY.md 初始化模板 | `onboard.go` 或 memory 包 | P1 |
| W5 | 内置 Skills（至少 cron/github/memory/weather） | 新建 `templates/skills/*/SKILL.md` | P2 |
| W6 | config 增加 `tools.exec` / `tools.web` / `data` / `trace` 段 | `config/schema.go` | P2 |
| W7 | Duration 类型（`"30m"` ↔ `time.Duration`） | `config/schema.go` | P3 |
| W8 | providers 从固定 struct 改为 `map[string]ProviderConfig` | `config/schema.go` | P3 |

---

---

## 完成状态总览（2026-05-24）

### ✅ 已完成（16/23）

| # | 项目 | 文件 |
|---|------|------|
| 1 | Session Manager | `internal/session/manager.go` |
| 2 | Memory Consolidation | `internal/agent/memory.go` (Consolidator) |
| 3 | Tool Wrapper | `internal/tool/wrapper.go` |
| 4 | Subagent 系统 | `internal/subagent/manager.go` |
| 5 | RunInboundLoop 增强 | `internal/agent/loop.go` (per-session workers) |
| 6 | Message Tool | `internal/tool/tools/message.go` |
| 7 | Cron Tool 接口 | `internal/tool/tools/cron.go` |
| 8 | TurnContext | `internal/tool/turnctx.go` |
| 9 | Spawn Tool | `internal/tool/tools/spawn.go` |
| 10 | Provider 扩展 (4→10) | `internal/provider/registry.go` |
| 11 | Prompt 模板完整内容 | `templates/*.md` + Go embed |
| 12 | Workspace 模板同步 | `internal/workspace/sync.go` |
| 13 | MEMORY/HISTORY 初始化 | `workspace.InitMemoryFiles()` |
| 14 | ProvidersConfig → map | `config/schema.go` |
| 15 | DataConfig + TracingConfig | `config/schema.go` |
| 16 | 两层记忆 (MEMORY+HISTORY) | `agent/memory.go` |

### ⚠️ 部分完成（2/23）

| # | 项目 | 已有 | 缺失 |
|---|------|------|------|
| 17 | Graceful Shutdown | signal→cancel→stop 基础流程 | 多阶段关闭 / RuntimeComponents / CancelAll 计数 |
| 18 | Config 字段补全 | DataConfig, TracingConfig | ExecConfig / WebConfig / Duration 类型 |

### ❌ 未实现（5/23）

| # | 项目 | 优先级 | 说明 |
|---|------|--------|------|
| 19 | MCP 集成 | P2 | MCP server 连接+工具发现 |
| 20 | Skills 完善 | P2 | JSON metadata / 依赖检查 / XML 摘要 |
| 21 | Prompt Loader 独立包 | P3 | 从文件组装 system prompt |
| 22 | Tracing (Langfuse) | P3 | 可观测性 |
| 23 | CLI REPL 完善 | P3 | liner + glamour (搁置) |

### 额外发现（agent.md 对比）— ✅ 10/10 已完成

---

## 新一轮深度对比（2026-05-24）

对比 `D:\projects\nanobot-go` vs `D:\projects\nanobot-golang`，逐函数、逐字段核对。

### 新增遗漏项

#### A. processMessage 未完整实现 🔴 P1

**loop.go** `processMessage()` 是空 stub，缺少：

| 功能 | 说明 |
|------|------|
| Langfuse tracing | 每个消息创建 trace |
| 瞬时错误重试 | `apperr.Retryable(err)` → 2s 后重试一次 |
| Stream 消费 + Outbound 发布 | agent.ChatStream → 收集内容 → 回发 OutboundMessage |
| 错误 public message 回退 | 最终错误通过 `apperr.PublicMessage(err)` 发给用户 |
| TurnContext 防重复 | `WasMessageSent()` 防止工具已发消息后重复发送 |
| ReplyTo 提取 | 从 metadata 提取 message_id 用于线程回复 |
| 系统路由解码 | `DecodeSystemRoute` 将 `system:channel:chatID` 解码 |

#### B. /new 缺少 ArchiveUnconsolidated 🔴 P1

我们的 `handleNewSession` 直接 `Clear()`，丢失未合并消息。
nanobot-go 在 Clear 前调用 `consolidator.ArchiveUnconsolidated(ctx, sess)` 把消息归档到长期记忆。

#### C. /restart (syscall.Exec) 🟡 P3

nanobot-go 通过 `syscall.Exec` 原地进程替换实现重启。我们没有。

#### D. Prompt Loader 未从文件加载 🔴 P1

我们的 `buildMessages` 从 memory/MEMORY.md 构建 bootstrap，而非从 prompts/ 目录的 SOUL.md/USER.md/TOOLS.md/AGENTS.md 文件加载。nanobot-go 的 `prompt.Loader.BuildSystemMessages()` 读取这5个文件组装为结构化系统消息。

#### E. Graceful Shutdown 编排 🟡 P2

nanobot-go 有完整的 `shutdown.go`（`RuntimeComponents` + `StartGracefulShutdown` + 多阶段关闭），我们只有 signal→cancel→stop 基础流程。

#### F. 配置加载器缺少 env var 绑定 🟡 P2

nanobot-go 有 `bindEnvVars()` 绑定 10 个环境变量（FEISHU_*/NANOBOT_*）。我们完全移除。

#### G. Provider 匹配缺少 DetectByBase 🟡 P2

nanobot-go 通过 `api_base` URL 子串检测自动识别 provider（Ollama=`"11434"`、Azure=`".openai.azure.com"`）。我们没有此策略。

#### H. Skill 系统缺少：JSON metadata + 依赖检查 + XML摘要 + install提示 🟡 P2

详见 plan1.md 第十章。

#### I. 缺少 provider：AiHubMix / Azure OpenAI / Moonshot / MiniMax / vLLM / Ark / Qianfan 🟢 P3

当前 10 个 vs nanobot-go 的 16 个。

#### J. MCP 惰性加载 🟡 P2

`ensureMCPConnected` 在首条消息时延迟连接 MCP server 并重建 react agent。

#### K. OnProgress 回调未接入 🟡 P2

nanobot-go 的 Agent 有 `OnProgress tools.ToolProgressFunc` 字段，tool 执行时回调。我们未接入。

---

### 最终状态（2026-05-25）

| 优先级 | # | 项目 | 状态 |
|--------|---|------|------|
| 🔴 P1 | A | processMessage 完整实现 | ✅ |
| 🔴 P1 | B | /new 的 ArchiveUnconsolidated | ✅ |
| 🔴 P1 | D | Prompt Loader 从文件加载 | ✅ |
| 🟡 P2 | E | Graceful Shutdown 编排 | ✅ |
| 🟡 P2 | F | Config 环境变量绑定 | ✅ 有意移除（配置从文件读取） |
| 🟡 P2 | G | Provider DetectByBase | ❌ 仅剩 |
| 🟡 P2 | H | Skill 系统完善 | ✅ |
| 🟡 P2 | J | MCP 惰性加载 | ✅ |
| 🟡 P2 | K | OnProgress 回调接入 | ✅ |
| 🟢 P3 | C | /restart (syscall.Exec) | ❌ 仅剩 |
| 🟢 P3 | I | 补充 7 个 provider | ❌ 仅剩 |

**已完成：34/37**

### 仅剩 3 项（均为低优先级）

| # | 项目 | 说明 |
|---|------|------|
| G | Provider DetectByBase | 通过 API base URL 自动识别 provider (Ollama `11434`, Azure `openai.azure.com`) |
| C | /restart (syscall.Exec) | 原地进程替换重启 |
| I | 补充 7 个 provider | AiHubMix / Azure / Moonshot / MiniMax / vLLM / Ark / Qianfan |
