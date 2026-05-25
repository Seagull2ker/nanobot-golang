# nanobot-golang 对比 nanobot-go 最终差异分析

> 基准：`D:\projects\nanobot-go`（83文件，18包，61项功能）
> 本项目：`D:\projects\nanobot-golang`（~65文件，20包，~10000行）

---

## 一、已对齐的功能（51/61）

| 类别 | 功能 |
|------|------|
| **入口** | gateway 完整启动、agent CLI（基础）、onboard 初始化、status 诊断、version |
| **LLM** | 10 个 provider、自动匹配、原生 tool calling、ChatStream 完整生命周期+MessageFuture |
| **工具** | 12 个内置工具（exec/read/write/edit/list_dir/web_fetch/web_search/apply_patch/message/cron/spawn + MCP 基础设施） |
| **工具包装** | WrapAllTools 截断+错误降级+进度回调、normalizeToolArguments |
| **记忆** | MEMORY.md+HISTORY.md 双层存储、LLM 驱动合并、token 预算触发、user-turn 边界、raw archive 回退、ArchiveUnconsolidated |
| **会话** | JSONL 持久化+缓存、LastConsolidated 索引、GetHistory 对齐 |
| **消息** | MessageBus 双缓冲、per-session worker 隔离、processMessage 完整实现（TurnContext+ChatStream+outbound+错误回退） |
| **通道** | 飞书（WebSocket+富文本+卡片分块+Markdown转换+@提及+群组策略）、WebSocket（gorilla） |
| **Agent** | Eino react.Agent+MessageModifier 治理、Prompt Loader（5文件→system message）、Skills（YAML+XML摘要+依赖检查+安装提示）、AgentHook/ProgressHook |
| **Cron** | CronService（every/cron/at 三种模式+JSON 持久化）、CronTool（add/list/remove） |
| **心跳** | HeartbeatService（定时回调） |
| **子Agent** | SubagentManager（Spawn+受限工具集+总线通知+CancelBySession+CancelAll） |
| **配置** | YAML/JSON 加载、AgentConfig 扁平化、FeishuChannelConfig、DataConfig+TracingConfig、Workspace Sync（Go embed） |
| **关闭** | StartGracefulShutdown 多阶段关闭、RuntimeComponents |
| **安全** | SSRF（DNS rebinding+CIDS）、Shell deny patterns 15条、path safety |
| **MCP** | ConnectMCPServer（stdio/SSE/streamableHttp）+ ensureMCPConnected 惰性加载 |

---

## 二、未实现的功能（10/61）

### 1. `/restart` 原地进程重启 🟢 P3

**nanobot-go 实现：** `agent.go:462-492`，`syscall.Exec(exe, os.Args, os.Environ())`，500ms 延迟等 ack 消息发出

**方案：** 在 `agent.go` 的 `ChatStream` 命令检测中添加 `/restart` 处理，Windows 不支持 `syscall.Exec`，用 `os.Exit(0)` + 外部进程管理器重启作为替代。

### 2. Heartbeat LLM 驱动决策 🟡 P2

**nanobot-go 实现：** `heartbeat/service.go` 定义 `heartbeat` 工具（action=skip/run），LLM 读取 HEARTBEAT.md 判断是否需要执行

**当前状态：** 我们的 HeartbeatService 只有定时回调，没有 LLM 决策层

**方案：**
1. 在 `HeartbeatService.Tick` 中读取 HEARTBEAT.md
2. 构造系统提示："检查 HEARTBEAT.md，用 heartbeat 工具决定 skip 还是 run"
3. 调用 chatModel.Generate，绑定 `heartbeat` 工具（`tool_choice=forced`）
4. 解析 LLM 返回的 action：skip → 什么都不做，run → 发布 InboundMessage 给 Agent

### 3. Web Search 多 provider 🟡 P2

**nanobot-go 实现：** `web.go` 支持 5 个搜索 provider：Brave、Tavily、SearXNG、Jina、DuckDuckGo，DDG 有两级回退（JSON API → HTML 抓取）

**当前状态：** 我们只实现了 DuckDuckGo

**方案：**
1. 在 `WebSearchConfig` 中添加 `Provider` 字段（可选值：brave/tavily/searxng/jina/duckduckgo）
2. 根据 Provider 配置分发到不同的搜索函数
3. Brave / Tavily / Jina 需要 API key，从 config 读取

### 4. Web Fetch Jina Reader 🟡 P2

**nanobot-go 实现：** `web.go` 优先使用 Jina Reader（`r.jina.ai/{url}`），返回 LLM 友好的 Markdown；429 时降级到直接 HTTP GET

**当前状态：** 我们只实现了直接 HTTP GET + HTML→Markdown 转换

**方案：** 在 `fetchDirect` 之前添加 `fetchViaJina(url)` 调用，成功直接返回，失败/429 降级到原逻辑

### 5. DetectByBase 提供商匹配 🟢 P3

**nanobot-go 实现：** `providers.go` 第5步匹配策略：检查 api_base URL 是否包含特征的检测子串（Ollama="11434", Azure=".openai.azure.com", AiHubMix="aihubmix"）

**当前状态：** 我们的 `Registry.Match` 只支持名称/关键词/前缀匹配

**方案：** 在 `Registry.Match` 中添加第4步：遍历 provider 检查 `DetectByBase` 字段是否在 api_base URL 中出现。需要给 ProviderSpec 添加 `DetectByBase string` 字段

### 6. 追加 7 个 provider 🟢 P3

缺失：AiHubMix / Azure OpenAI / Moonshot(Kimi) / MiniMax / vLLM / Ark(Doubao) / Qianfan(ERNIE)

方案：全部使用 `openai_compat` 后端，只需在 `DefaultRegistry()` 中添加 `ProviderSpec` 注册项

### 7. Langfuse Tracing 🟢 P3

**nanobot-go 实现：** `trace/trace.go` + `trace/span.go`，全局注册 Eino 回调，`StartSpan/EndSpan` 手动 span

**方案：** 按需引入 `eino-ext/callbacks/langfuse`，在 gateway 启动时调用 `trace.Init(cfg.Trace)`，无需改动核心代码

### 8. CLI REPL 完善 🟢 P3（搁置）

**nanobot-go 实现：** `cmd/cli/main.go`，`peterh/liner` 行编辑+历史、`charmbracelet/glamour` Markdown 渲染、spinner 旋转动画、流式逐 token 输出

**方案：** 待 gateway 链路跑通后再完善

### 9. apperr PublicMessage 🟢 P3

**nanobot-go 实现：** `pkg/apperr/apperr.go` 每种 Kind 都有中文 `Public` 消息（如 "抱歉，请求超时了，请稍后再试。"）

**方案：** 在 `internal/errors/errors.go` 中给 Error 增加 Public 字段和 `PublicMessage()` 方法

### 10. Config Duration 类型 🟢 P3

**nanobot-go 实现：** `schema.go` 自定义 `Duration` 类型，JSON 支持 `"30m"` 字符串或数字秒数

**方案：** 当前用 `int` 秒已够用，后续如需字符串配置再加

---

## 三、优先级建议

| 优先级 | 数量 | 项目 |
|--------|------|------|
| 🟡 P2 | 3 | Heartbeat LLM 决策 / Web Search 多 provider / Web Fetch Jina Reader |
| 🟢 P3 | 7 | /restart / DetectByBase / 7 个 provider / Tracing / CLI REPL / apperr / Duration |

---

## 四、技术实现方案

### P2-1：Heartbeat LLM 驱动决策

**涉及文件：**
- `internal/heartbeat/service.go` — 增加 `Tick` 方法，接收 chatModel
- `cmd/nanobot/gateway.go` — 心跳初始化时传入 chatModel

**实现步骤：**
1. HeartbeatService 增加字段：`chatModel`, `heartbeatPath`
2. `New` 增加参数 `chatModel model.BaseChatModel`, `heartbeatPath string`
3. `Tick(ctx)` 方法：
   - 读取 `heartbeatPath` 文件（HEARTBEAT.md），空文件跳过
   - 定义 `heartbeat` 工具：action(skip/run), tasks(string)
   - 构造 system prompt + user content (HEARTBEAT.md 内容)
   - `chatModel.Generate(ctx, messages, model.WithTools(...), model.WithToolChoice("required"))`
   - 解析 tool call 结果：skip → 日志, run → 发布 InboundMessage

### P2-2：Web Search 多 provider

**涉及文件：**
- `internal/tool/tools/web.go` — 添加 provider 分发逻辑
- `internal/config/schema.go` — ToolsConfig 添加 WebConfig

**实现步骤：**
1. 添加 `WebSearchProvider` 接口和 provider 注册 map
2. 实现 `SearchProvider` 接口：`Search(ctx, query, count) ([]SearchResult, error)`
3. DuckDuckGo / Brave / Tavily / Jina 各实现该接口
4. `WebFetchTool` 的 `Execute` 根据 config 选择 provider 调用

### P2-3：Web Fetch Jina Reader

**涉及文件：**
- `internal/tool/tools/web.go`

**实现步骤：**
1. 添加 `fetchViaJina(ctx, url)` 函数：`GET https://r.jina.ai/{url}`
2. 在 `fetchDirect` 之前调用：成功 → 返回 Markdown 结果，429/失败 → 降级到 fetchDirect

---

## 五、总结

**核心功能完整度：83.6%（51/61）**

10 项未实现的功能全部是 P2-P3 级别，不影响核心链路。其中 3 项 P2 值得近期完成（Heartbeat LLM / Web Search / Jina Reader），7 项 P3 可在后续迭代中按需实现。
