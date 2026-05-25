# PROGRESS.md

Last updated: 2026-05-24

## Overall Status

Phase 0 ✅, Phase 1 ✅, Phase 2 ✅, Phase 3 ✅, Phase 4 ✅, Phase 5 CLI 骨架完成。全部 build + test 通过。项目核心框架完毕。

## Phase 0: 基础设施 — ✅ 完成

| # | 任务 | 文件 | 行数 | 状态 |
|---|------|------|------|------|
| 0.1 | Go Module 初始化 | `go.mod`, `Makefile`, `.gitignore`, `.golangci.yml` | — | ✅ |
| 0.2 | 结构化错误系统 | `internal/errors/errors.go` | 168 | ✅ |
| 0.3 | 日志 (slog) | `internal/log/log.go` | 26 | ✅ |
| 0.4 | 共享类型 | `internal/types/messages.go` (47), `tools.go` (67), `sessions.go` (14) | 128 | ✅ |
| 0.5 | MessageBus | `internal/bus/bus.go` | 77 | ✅ |
| 0.6 | 配置系统 | `internal/config/schema.go` (336), `loader.go` (134), `paths.go` (99) | 569 | ✅ |
| 0.6 | 配置测试 | `internal/config/config_test.go` | 150 | ✅ 7/7 pass |
| — | CLI 入口 | `cmd/nanobot/main.go` (72), `onboard.go` (68), `status.go` (111) | 251 | ✅ |
| — | CLI 占位 | `cmd/nanobot/agent.go` (37), `gateway.go` (25) | 62 | ⏳ 占位 |

**总计：15 个 Go 文件，1431 行**

**验证：** `go build ./...` 通过，`go test ./...` 7/7 通过，`go vet ./...` 零警告

### 已知 gap

- ~~`loader.go` 实际使用 `encoding/json`，plan.md 声称已迁移 viper。~~ ✅ 已迁移：Load() 使用 viper 读取 JSON + mapstructure (json tag)，applyEnvOverrides 保留手动绑定，Save() 保留 encoding/json

## Phase 1: Provider 子系统 — ✅ 完成（2026-05-24）

| # | 任务 | 文件 | 行数 | 状态 |
|---|------|------|------|------|
| 1.1 | ChatModelAdapter 接口 | `internal/provider/provider.go` | 263 | ✅ |
| 1.2 | ProviderSpec + Registry | `internal/provider/registry.go` | 165 | ✅ |
| 1.3 | OpenAI 兼容适配器 | `internal/provider/openai.go` | 405 | ✅ |
| 1.4 | Anthropic Messages 适配器 | `internal/provider/anthropic.go` | 531 | ✅ |
| 1.5 | Provider 工厂 | `internal/provider/factory.go` | 51 | ✅ |
| 1.6 | 重试逻辑 | `internal/provider/retry.go` | 104 | ✅ |
| 1.7 | 断路器 + Fallback | `internal/provider/fallback.go` | 133 | ✅ |
| — | Provider 测试 | `internal/provider/provider_test.go` | 156 | ✅ 10/10 pass |
| — | Viper 迁移 | `internal/config/loader.go` (viper), `internal/config/schema.go` (+ByProviderName) | — | ✅ |

**总计：8 个新文件，1808 行，10 测试**

**累计：23 个 Go 文件，~3239 行，17 测试**

**验证：** `go build ./...` 通过，`go test ./...` 17/17 通过，`go vet ./...` 零警告

**依赖：** `cloudwego/eino v0.8.13`, `sashabaranov/go-openai v1.41.2`, `cenkalti/backoff/v4`, `sony/gobreaker/v2`

### 设计要点

- ChatModelAdapter 嵌入 `model.BaseChatModel`（Generate + Stream），增加 GetDefaultModel/SupportsThinking
- Registry 支持按 provider 名 + 模型关键词 + 前缀匹配
- OpenAI 兼容适配器同时服务 OpenAI/DeepSeek/DashScope（通过 BackendType 路由）
- Thinking 注入：ThinkingType (DeepSeek), ThinkingEnabled (DashScope), ThinkingReasoningSplit (OpenAI)
- 重试：standard（4次尝试，1s→2s→4s） / persistent（无限+指数退避+60s上限+10次相同错误停止）
- 断路器：连续3次失败→60s冷却→半开探测
- Fallback 链：遍历候选列表，即时创建适配器

## Phase 2: Tool 子系统 — ✅ 完成（2026-05-24）

| # | 任务 | 文件 | 行数 | 状态 |
|---|------|------|------|------|
| 2.1 | Tool 接口 + Schema 类型 | `internal/tool/tool.go` (20), `schema_types.go` (337) | 357 | ✅ |
| 2.2 | ToolRegistry (init自注册) | `internal/tool/registry.go` | 133 | ✅ |
| 2.3 | LocalSandbox | `internal/tool/sandbox.go` | 79 | ✅ |
| 2.4 | 路径安全 + 文件追踪 | `internal/tool/path_utils.go` (64), `file_state.go` (135) | 199 | ✅ |
| 2.5 | Shell 工具 | `internal/tool/tools/shell.go` | 100 | ✅ |
| 2.6 | 文件系统工具 | `internal/tool/tools/filesystem.go` (read/write/edit/list) | 306 | ✅ |
| 2.7 | Web 工具 | `internal/tool/tools/web.go` (web_fetch/web_search) | 230 | ✅ |
| — | Tool 测试 | `internal/tool/tool_test.go` | 152 | ✅ 7/7 pass |

**总计：10 个新文件，1556 行，7 测试**

**累计：33 个 Go 文件，~4795 行，24 测试**

**验证：** `go build ./...` 通过，`go test ./...` 24/24 通过，`go vet ./...` 零警告

### 设计要点

- Tool 接口：同步 Execute(ctx, params) → (*Result, error)，含 Name/Description/Parameters/ReadOnly/ConcurrencySafe/Exclusive
- 6 种 Schema 类型：String/Integer/Number/Boolean/Array/Object，各含 Validate() + ToJSONSchema()
- init() 自注册到全局 Registry（参照 Python pkgutil 自动发现，Go 用编译时注册）
- Definitions() 输出 OpenAI function 格式，带缓存
- LocalSandbox：os/exec + context timeout + stdout/stderr/exitCode
- 路径安全：ResolveFilePath + ValidatePathSafety（workspace 外拒绝访问）
- FileState：per-session 读写追踪 + 去重 + staleness 检测
- WebFetch：SSRF 防护（阻止内网/私有地址），HTML→文本，1MB 限制，16000 字符截断

## Phase 3: Agent Core — ✅ 完成（2026-05-24）

| # | 任务 | 文件 | 行数 | 状态 |
|---|------|------|------|------|
| 3.1 | ReAct Agent 组装 | `internal/agent/agent.go` | 175 | ✅ |
| 3.2 | AgentLoop + Runner | `internal/agent/loop.go` (69), `runner.go` (131) | 200 | ✅ |
| 3.3 | MemoryStore | `internal/agent/memory.go` | 91 | ✅ |
| 3.4 | ContextBuilder | `internal/agent/context.go` | 89 | ✅ |
| 3.5 | SkillsLoader | `internal/agent/skills.go` | 144 | ✅ |
| 3.6 | AgentHook | `internal/agent/hook.go` | 101 | ✅ |
| 3.7 | Dream | `internal/agent/dream.go` | 81 | ✅ |
| 3.8 | ProgressHook | `internal/agent/progress.go` | 60 | ✅ |
| — | Provider 更新 | 4 文件 +BindTools | — | ✅ |

**总计：9 个新文件，941 行**

**累计：42 个 Go 文件，~5736 行，24 测试**

**验证：** `go build ./...` 通过，`go test ./...` 24/24 通过，`go vet ./...` 零警告

### 设计要点

- 使用 Eino `react.NewAgent`（非 adk.ChatModelAgent），与 nanobot-go 项目一致
- `einoToolAdapter` 将 nanobot Tool 接口适配为 Eino `InvokableTool`
- Governance 通过 `MessageModifier` 实现：dropOrphan / backfill / microcompact（每次 LLM 调用前执行）
- ChatModelAdapter 扩展 `model.ChatModel`（含 BindTools），4 个适配器均已实现
- AgentLoop：bus 驱动的 per-session worker 模式 + 任务取消
- AgentRunner：空响应 retry（最多 2 次）+ length 截断 recovery（最多 3 次）
- MemoryStore：MEMORY.md + history.jsonl 两层层存储
- ContextBuilder：Identity → Bootstrap → Memory → Skills 四段式拼接 + RuntimeContext
- SkillsLoader：YAML frontmatter 解析 + workspace/builtin 双层回退
- CompositeHook：扇出 + 错误隔离 + FinalizeContent 管道
- Dream：两阶段记忆处理框架（Phase1 读取未处理事件，Phase2 AgentRunner 编辑 MEMORY.md）

预估 7-10 天。Eino ChatModelAgent + 自定义中间件 + Memory + Dream + ContextBuilder + SubAgent。

## Phase 4: Channel + 子系统 — ✅ 完成（2026-05-24）

| # | 任务 | 文件 | 行数 | 状态 |
|---|------|------|------|------|
| 4.1 | Channel 接口 + Manager | `internal/channel/base.go` (17), `manager.go` (77) | 94 | ✅ |
| 4.2 | WebSocket Channel | `internal/channel/websocket.go` | 154 | ✅ |
| 4.3 | 飞书 Channel | `internal/channel/feishu.go` | 270 | ✅ (SDK接入) |
| 4.4 | CronService | `internal/cron/service.go` | 186 | ✅ |
| 4.5 | HeartbeatService | `internal/heartbeat/service.go` | 60 | ✅ |
| 4.6 | CommandRouter | `internal/command/router.go` (5 内置命令) | 120 | ✅ |
| 4.7 | API Server | `internal/api/server.go` (OpenAI 兼容) | 140 | ✅ |

**总计：8 个新文件，794 行**

**累计：50 个 Go 文件，~6530 行，24 测试**

**验证：** `go build ./...` 通过，`go test ./...` 24/24 通过，`go vet ./...` 零警告

## Phase 5: CLI + 打磨 — ⏳ 骨架完成

| 命令 | 状态 | 说明 |
|------|------|------|
| `version` | ✅ | 打印版本信息 |
| `onboard` | ✅ | 初始化 `~/.nanobot/` 目录树 + 默认配置 |
| `status` | ✅ | 打印 7 个区段的完整配置诊断 |
| `agent` | ✅ | 单消息模式可用 + REPL 框架（接入真实 ChatModel + Tools + Agent） |
| `gateway` | ✅ | 完整集成入口（provider + agent + tools + channels + cron + heartbeat + API + command router） |

### Security 子系统 + CLI 完善 ✅ (2026-05-24)

| 文件 | 说明 |
|------|------|
| `internal/security/ssrf.go` | 共享 SSRF 防护（IsBlockedIP + ValidateURLTarget + SSRFError），web.go 重复代码已消除 |
| `cmd/nanobot/gateway.go` | 从占位→完整集成（10步启动：bus→chatModel→tools→agent→loop→channels→cron→heartbeat→api→commands） |
| `cmd/nanobot/agent.go` | 从占位→可用（接入真实 ChatModel + Tools + ReAct Agent，单消息模式 + REPL 框架） |

### Phase 2 安全/质量补全 ✅ (2026-05-24)

| 文件 | 改进项 | 行数 |
|------|--------|------|
| `shell.go` | 15 deny patterns (rm -rf/format/mkfs/dd/shutdown/fork bomb) + internal URL blocking | 140 |
| `filesystem.go` | edit 空格容错 + LCS 相似度诊断 + list_dir recursive + noise-dir跳过(.git/node_modules等15目录) + read_file 128K maxChars + 续读提示 | 482 |
| `web.go` | DuckDuckGo search + DNS rebinding SSRF (10 CIDR段逐IP检查) + HTML→Markdown + untrusted banner | 452 |

**验证：** `go build ./...` 通过，`go test ./...` 24/24 通过，`go vet ./...` 零警告

## 下一步（按优先级）

1. ~~P0-P2 全部完成~~ ✅ ~~gateway 链路跑通~~ ✅ ~~43项差异清除~~ ✅ ~~文档完成~~ ✅
2. 文档：`docs/` 目录 8 个 .md（overview/config/provider/tools/agent/channels/storage/cli）
5. agent 命令完善（REPL + glamour + liner）— 搁置
