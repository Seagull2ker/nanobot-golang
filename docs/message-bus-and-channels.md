# 消息总线与通道系统 (pkg/bus/ + pkg/channels/)

## 概述

消息总线（MessageBus）是 Agent 和外部通道之间的解耦层。通道适配器将外部消息（飞书、CLI 等）发布到总线，Agent 处理后通过总线返回响应，通道适配器再发送给用户。

## 文件结构

```
pkg/bus/
└── bus.go           # MessageBus — 核心消息总线

pkg/channels/
├── base.go          # Channel 接口 + 访问控制
└── feishu.go        # FeishuChannel — 飞书通道适配器

pkg/app/
├── runloop.go       # RunInboundLoop — 消息消费主循环
├── feishu.go        # 飞书通道启动
└── shutdown.go      # 优雅关闭
```

---

## 1. MessageBus (pkg/bus/bus.go)

### 数据结构

```go
type MessageBus struct {
    inbound      chan *InboundMessage   // 容量100的缓冲channel
    outbound     chan *OutboundMessage  // 容量100的缓冲channel
    inboundOnce  sync.Once
    outboundOnce sync.Once
}
```

MessageBus 本质是两个带缓冲的 Go channel：
- **inbound**: 消息从通道流向 Agent
- **outbound**: 消息从 Agent 流向通道

### InboundMessage — 入站消息

```go
type InboundMessage struct {
    Channel            string         // 来源通道: "feishu", "cli", "system", "heartbeat"
    SenderID           string         // 发送者标识
    ChatID             string         // 会话标识
    Content            string         // 消息正文
    Timestamp          time.Time
    Media              []string       // 附件路径
    Metadata           map[string]any // 元数据 (message_id 等)
    SessionKeyOverride string         // 覆盖默认 SessionKey
}
```

### SessionKey 计算

```go
func (m *InboundMessage) SessionKey() string {
    if m.SessionKeyOverride != "" {
        return m.SessionKeyOverride  // 子任务返回时用
    }
    return m.Channel + ":" + m.ChatID  // 默认: "feishu:oc_xxx"
}
```

### OutboundMessage — 出站消息

```go
type OutboundMessage struct {
    Channel  string         // 目标通道
    ChatID   string         // 目标会话
    Content  string         // 消息内容
    ReplyTo  string         // 回复目标消息ID
    Media    []string       // 附件
    Metadata map[string]any
}
```

### 关键方法

| 方法 | 说明 |
|------|------|
| `NewMessageBus()` | 创建容量为100的缓冲总线 |
| `PublishInbound(ctx, msg)` | 发布入站消息（非阻塞, select + default） |
| `PublishOutbound(ctx, msg)` | 发布出站消息 |
| `ConsumeInbound(ctx)` | 返回 inbound 通道（用于 range 消费） |
| `ConsumeOutbound(ctx)` | 返回 outbound 通道 |
| `Close()` | 关闭 inbound 通道（触发 RunInboundLoop 退出） |
| `CloseOutbound()` | 关闭 outbound 通道 |

**关闭顺序很重要**: 先 `Close()` inbound → 等待所有 worker 完成 → 再 `CloseOutbound()`

---

## 2. RunInboundLoop — 消息消费主循环 (app/runloop.go)

```go
func RunInboundLoop(ctx, messageBus, bot, wg)
```

### 设计：Per-Session Worker

```
messageBus.Inbound
    │
    ▼
for msg := range inbound {
    key := msg.SessionKey()          ← 计算 session key
    sq := sessions.LoadOrStore(key)  ← 获取或创建该 session 的队列
    │
    ├── 已存在: 直接投入队列
    └── 新 session: 创建 goroutine
            │
            ▼
        for m := range q.ch {        ← 该 session 的 worker
            processMessage(ctx, bus, bot, m)
        }
}
```

**关键设计要点**:
- 同一 session 的消息**串行处理**（一个 goroutine + channel）
- 不同 session 的消息**并行处理**（不同 goroutine）
- 这保证了同一会话的消息顺序，同时不同会话互不阻塞
- 每个 session 的队列容量为 32

### processMessage — 单条消息处理

```go
func processMessage(ctx, messageBus, bot, m)
```

```
1. 确定 targetChannel / targetChatID
   └─ 若 channel=="system" → DecodeSystemRoute(chatID) 解码真实目标

2. 设置 Langfuse Trace (sessionID, userID, metadata)

3. 创建 TurnContext
   └─ 注入 ProgressInfo (channel, chatID)
   └─ 若 sender=="subagent" → 注入 InputRole="assistant"

4. bot.ChatStream(turnCtx, sessionID, content)
   ├── 失败 → 判断是否可重试
   │   ├── 可重试(Unavailable/Timeout/Network) → sleep 2s 重试
   │   └── 不可重试 → 发送错误消息到 outbound
   └── 成功 → 消费 stream

5. 消费 stream 消息
   ├── 拼接 fullResponse
   └── stream 错误处理

6. 发送响应
   ├── 如果 turnFlag.WasMessageSent() → message 工具已发送, 不重复
   ├── 如果 fullResponse == "" 且 stream 失败 → 发送错误
   └── 正常 → PublishOutbound(fullResponse)
```

### DecodeSystemRoute — 系统路由解码

```go
func DecodeSystemRoute(chatID string) (channel, targetChatID string)
```

用于子任务返回等场景，格式为 `"channel:chatID"`。

---

## 3. Channel 接口 (pkg/channels/base.go)

```go
type Channel interface {
    Start(ctx context.Context, handler Handler) error
    Stop(ctx context.Context) error
}
```

### 访问控制

```go
func IsSenderAllowed(channelName, senderID string, allowFrom []string) bool
```

| allowFrom | 行为 |
|-----------|------|
| `[]` | **拒绝所有人**（并警告） |
| `["*"]` | **允许所有人** |
| `["id1", "id2"]` | 只允许精确匹配 |

```go
func ValidateAllowFrom(channelName string, allowFrom []string) error
```
启动时调用：如果 allowFrom 为空，直接返回错误阻止通道启动。

---

## 4. FeishuChannel — 飞书通道 (pkg/channels/feishu.go)

### 生命周期

```
NewFeishuChannel(cfg, bus)
    │
    ▼
Start(ctx)
    ├── 创建 EventDispatcher (处理消息事件)
    ├── 创建 WebSocket Client
    └── 启动 WebSocket 连接 (goroutine)
    │
    ▼
ListenOutbound(ctx)         ← 消费 outbound 消息
    │   goroutine 中运行
    │
    ▼
Stop(ctx)
    └── 取消 WS Context, 等待 goroutine 退出
```

### 消息处理 (onMessage)

```
1. 去重: 通过 message_id 在 sync.Map 中去重
2. 访问控制: IsSenderAllowed
3. 内容解析:
   ├── text → 提取 text 字段
   ├── post → 富文本提取 (extractPostText)
   ├── interactive → 交互卡片提取 (extractInteractiveContent)
   ├── share_chat / share_user / share_calendar_event → 格式化
   └── 其他 → "[{msg_type}]"
4. 群策略判断:
   └── groupPolicy="mention" (默认) → 只有 @了机器人 的消息才处理
   └── groupPolicy="open" → 所有消息都处理
5. 标准化文本: 去掉 @bot 前缀 (normalizeFeishuText)
6. PublishInbound → 发布到消息总线
```

### 消息回复 (SendMessage)

```
1. buildFeishuCardContents(content, metadata)
   └── convertMarkdownToFeishu (Markdown → 飞书卡片格式)
   └── splitFeishuMarkdownContent (超长内容分片, 每片3000字符)

2. 回复模式 (reply):
   ├── 尝试 ReplyMessage (线程回复)
   └── 失败 → 降级为 CreateMessage (新建消息)

3. 新建模式 (create):
   └── CreateMessage (发送交互式卡片)
```

### Markdown → 飞书卡片格式转换

飞书卡片只支持有限的 Markdown 子集，转换规则：

| 原格式 | 飞书格式 |
|--------|----------|
| `# Heading` | `**Heading**` (粗体) |
| `## Heading` | `**Heading**` |
| `| table |` | `**header:** value \| **header:** value` |
| `> quote` | `*quote*` (斜体) |

### 消息内容提取

`extractPostText`: 从飞书富文本消息中提取纯文本
- 支持 `text`, `a`, `at` 标签
- 支持多语言格式（zh_cn, en_us, ja_jp）
- 处理嵌套 content blocks

`extractInteractiveContent`: 从交互卡片中提取文本
- 递归处理 `div`, `markdown`, `button`, `img`, `note`, `column_set` 等元素
- 提取链接和文本

---

## 5. 完整消息流

```
用户 (飞书)
    │
    ▼
飞书 WebSocket 接收消息
    │
    ▼
onMessage: 解析、去重、访问控制
    │
    ▼
bus.PublishInbound(inboundMsg)
    │
    ▼
RunInboundLoop: 按 session key 分配到 worker
    │
    ▼
processMessage: Langfuse Trace, TurnContext, ChatStream
    │
    ▼
Agent.ChatStream: ReAct 循环
    │
    ▼
[stream chunk...] → fullResponse
    │
    ▼
bus.PublishOutbound(outboundMsg)
    │
    ▼
FeishuChannel.ListenOutbound: 消费 outbound
    │
    ▼
SendMessage: Markdown→飞书卡片, 发送
    │
    ▼
用户 (飞书) 收到回复
```
