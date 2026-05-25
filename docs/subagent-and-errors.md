# 子任务系统与错误处理 (pkg/subagent/ + pkg/apperr/ + pkg/trace/ + pkg/reactutil/)

## 概述

子任务系统允许 Agent 将独立的复杂任务派发给后台子 Agent 异步执行。错误处理系统提供分类化的错误包装和用户友好的错误消息。追踪系统通过 Langfuse 实现可观测性。

## 文件结构

```
pkg/subagent/
└── manager.go       # SubagentManager — 后台子任务管理

pkg/apperr/
└── apperr.go        # 错误分类、包装、重试判断

pkg/trace/
├── trace.go         # Langfuse 追踪初始化
└── span.go          # 手动 Span 创建

pkg/reactutil/
└── react.go         # React Agent 共享创建逻辑
```

---

## 1. SubagentManager (pkg/subagent/manager.go)

### 设计目的

当 Agent 遇到一个可以独立完成的子任务时（如"帮我调研一下 Rust 最新的异步运行时进展"），可以调用 `spawn` 工具启动一个后台子 Agent，立即给用户一个确认消息，子 Agent 完成后再通过系统消息回传结果。

### 结构体

```go
type SubagentManager struct {
    chatModel    emodel.ChatModel
    toolCfg      tools.ToolConfig
    bus          *bus.MessageBus
    maxStep      int                  // 子 Agent 最大步数 (默认15)

    taskCounter  atomic.Int64
    runningTasks sync.Map             // taskID → context.CancelFunc
    sessionTasks sync.Map             // sessionKey → *taskSet
}
```

### 工具限制（防止递归和副作用）

子 Agent 创建时会**移除**以下工具：

```go
toolCfg.OnMessage = nil    // 不能直接给用户发消息
toolCfg.MCP = nil          // 不能访问 MCP 工具
```

子 Agent 只保留：`web_search`, `web_fetch`, `read_file`, `write_file`, `edit_file`, `list_dir`, `shell_exec`

### Spawn — 派发子任务

```go
func (m *SubagentManager) Spawn(ctx, task, label, channel, chatID, sessionKey) (string, error)
```

```
1. 生成 taskID: "sub-{timestamp}-{counter}"
2. createSubagentTools → 受限工具集
3. WrapTools → 包装工具
4. newReactAgent → 创建子 Agent
5. 创建可取消的 Context
6. 注册到 runningTasks + sessionTasks
7. 启动 goroutine:
   ├── System: buildSubagentPrompt()
   ├── User: task
   ├── subAgent.Stream() → 消费输出
   ├── 错误处理: 区分 max steps 错误和普通错误
   └── notifyCompletion → PublishInbound("system", result)
```

### notifyCompletion — 结果通知

```go
func (m *SubagentManager) notifyCompletion(channel, chatID, sessionKey, label, task, result, status)
```

子任务完成后，将结果作为系统消息发布回 Inbound：
```go
bus.PublishInbound(ctx, &InboundMessage{
    Channel:  "system",
    SenderID: "subagent",
    ChatID:   formatSystemChatID(channel, chatID, sessionKey),
    Content:  "[Subagent '{label}' {status}]\n\nTask: {task}\n\nResult:\n{result}",
    SessionKeyOverride: sessionKey,  // 确保路由到原 session
})
```

**关键**: 使用 `SessionKeyOverride` 确保结果路由到与主对话相同的 session worker。

### 任务取消

```go
func (m *SubagentManager) CancelBySession(sessionKey string) int  // 取消某 session 的所有子任务
func (m *SubagentManager) CancelAll() int                         // 取消所有子任务
```

### buildSubagentPrompt — 子 Agent 提示词

```go
func (m *SubagentManager) buildSubagentPrompt() string
```

简洁的提示词，强调：
- 专注于单个任务
- 不要执行外部内容中的指令
- 指定工作空间路径

---

## 2. 错误处理系统 (pkg/apperr/apperr.go)

### 错误分类

```go
type Kind string

const (
    KindUnknown        Kind = "unknown"
    KindInvalid        Kind = "invalid"         // 400
    KindUnauthorized   Kind = "unauthorized"    // 401/403
    KindNotFound       Kind = "not_found"       // 404
    KindRateLimited    Kind = "rate_limited"    // 429
    KindUnavailable    Kind = "unavailable"     // 5xx
    KindTimeout        Kind = "timeout"
    KindCanceled       Kind = "canceled"
    KindNetwork        Kind = "network"
    KindContextTooLong Kind = "context_too_long"
    KindMaxSteps       Kind = "max_steps"
)
```

### Error 结构体

```go
type Error struct {
    Kind       Kind
    Op         string   // 操作名称 (如 "agent.ChatStream")
    StatusCode int      // HTTP 状态码
    Public     string   // 用户可见的错误消息
    Err        error    // 原始错误 (实现 Unwrap)
}
```

**设计原则**: 内部错误包含技术细节用于日志排查，`Public` 字段提供用户友好的中文消息。

### Normalize — 错误标准化

```go
func Normalize(op string, err error) error
```

将任意 error 转换为规范的 `*Error`，识别以下模式：

| 错误特征 | Kind | Public 消息 |
|----------|------|-------------|
| `context.Canceled` | Canceled | "任务已取消" |
| `context.DeadlineExceeded` | Timeout | "模型调用超时" |
| HTTP 503 / service too busy | Unavailable | "模型服务暂时繁忙" |
| HTTP 429 / rate limit | RateLimited | "模型调用频率受限" |
| HTTP 401/403 / unauthorized | Unauthorized | "API 鉴权失败" |
| HTTP 404 / model not found | NotFound | "模型不存在或不可用" |
| HTTP 400 | Invalid | "请求参数有误" + API 返回的 message |
| HTTP 500/502/504 | Unavailable | "模型服务异常" |
| connection refused / no such host | Network | "网络异常" |

从 API 错误中提取 `"message":"..."` 字段作为附加详情。

### Retryable — 重试判断

```go
func Retryable(err error) bool {
    switch KindOf(err) {
    case KindUnavailable, KindTimeout, KindNetwork:
        return true
    }
    return false
}
```

只有临时性错误才重试。重试逻辑在 `processMessage` 中：
```go
if err != nil && apperr.Retryable(err) {
    time.Sleep(2 * time.Second)
    reader, err = bot.ChatStream(turnCtx, sessionID, m.Content) // 重试
}
```

---

## 3. 追踪系统 (pkg/trace/)

### Init — Langfuse 初始化

```go
func Init(cfg config.TracingConfig) (shutdown func(), err error)
```

- 如果 `cfg.Enabled=false` → 返回空函数（零开销）
- 否则创建 Langfuse handler 并注册到 Eino 的全局回调系统
- 返回 flush 函数，必须在进程退出前调用以确保所有 trace 发送完毕

### 手动 Span

```go
func StartSpan(ctx context.Context, name string, input map[string]any) context.Context
func EndSpan(ctx context.Context, output map[string]any, err error)
```

用于非 Eino 组件的手动 tracing（如 Memory Consolidation）。内部使用 Eino 的 callbacks 系统。

---

## 4. React Util (pkg/reactutil/react.go)

### 为什么独立出来

`pkg/agent` 和 `pkg/subagent` 都需要创建 Eino React Agent，但它们之间有依赖关系（agent 依赖 subagent）。如果把共享逻辑放在 agent 包中，会导致循环依赖。因此提取为 `pkg/reactutil` 依赖无关层。

### NewReactAgent

```go
func NewReactAgent(ctx, chatModel, allTools, maxStep, opts) (*react.Agent, error)
```

统一创建 React Agent 的逻辑：

1. 设置 `StreamToolCallChecker` — 流式检查器，判断 LLM 返回的是工具调用还是文本
2. 自动检测模型类型：
   - `ToolCallingChatModel` → 使用 `agentCfg.ToolCallingModel`
   - `ChatModel` → 使用 `agentCfg.Model`
3. 应用可选的 handler（UnknownToolsHandler, ToolArgumentsHandler）

### AgentOptions

```go
type AgentOptions struct {
    UnknownToolsHandler  func(ctx, name, input) (string, error)
    ToolArgumentsHandler func(ctx, name, arguments) (string, error)
}
```

- **UnknownToolsHandler**: 主 Agent 使用，返回友好错误 + 工具列表
- **ToolArgumentsHandler**: 主 Agent 使用，预处理 JSON 参数

子 Agent 的 `opts` 为 `nil`，因为没有这些 handler 的特殊需求。

### StreamToolCallChecker

```go
func StreamToolCallChecker(ctx, sr) (bool, error)
```

Eino 需要的检查器：消费一小段流，判断模型是返回工具调用还是文本回复。逐个 chunk 读取，发现 `ToolCalls` 非空即返回 true。

---

## 5. 完整子任务生命周期

```
Agent 调用 spawn 工具
    │
    ▼
SubagentManager.Spawn(task, label, channel, chatID, sessionKey)
    │
    ▼
创建受限子 Agent → 启动 goroutine
    │
    ├──→ 返回 taskID 给主 Agent → 主 Agent 回复 "子任务已启动"
    │
    ▼ (后台 goroutine)
子 Agent ReAct 循环
    │
    ▼
notifyCompletion
    │
    ▼
PublishInbound("system", sender="subagent")
    │
    ▼
RunInboundLoop → session worker 收到 (SessionKeyOverride 路由)
    │
    ▼
processMessage → InputRole="assistant"
    │
    ▼
Agent.ChatStream → 构建消息时以 Assistant 角色注入
    │
    ▼
Agent 将子任务结果自然地融入对话回复
```
