# 定时任务与心跳 (pkg/cron/ + pkg/heartbeat/)

## 概述

Cron 服务提供 Agent 可管理的定时任务系统，Heartbeat 服务提供周期性健康检查。两者由 `pkg/app/cron.go` 和 `pkg/app/heartbeat.go` 组装到 Gateway 中。

## 文件结构

```
pkg/cron/
└── service.go       # CronService — 定时任务引擎

pkg/heartbeat/
└── service.go       # HeartbeatService — 周期性 LLM 检查

pkg/app/
├── cron.go          # BuildCronJobHandler — 组装 cron → bus
└── heartbeat.go     # StartHeartbeatService — 组装 heartbeat → bus
```

---

## 1. CronService (pkg/cron/service.go)

### 核心依赖

使用 `github.com/robfig/cron/v3` 作为底层调度引擎（支持秒级精度）。

### 数据结构

```go
type CronJob struct {
    ID             string       // UUID 前8位
    Name           string
    Enabled        bool
    Schedule       CronSchedule
    Payload        CronPayload
    State          CronJobState
    CreatedAtMs    int64
    UpdatedAtMs    int64
    DeleteAfterRun bool         // 一次性任务完成后自动删除
}
```

### 三种调度方式

```go
type CronSchedule struct {
    Kind    JobKind  // 三种之一
    AtMs    int64    // JobKindAt: Unix 毫秒时间戳
    EveryMs int64    // JobKindEvery: 间隔毫秒
    Expr    string   // JobKindCron: 标准 cron 表达式(6字段含秒)
    TZ      string   // 时区 (配合 cron 表达式)
}
```

| Kind | 说明 | 示例 | 实现方式 |
|------|------|------|----------|
| `at` | 一次性定时 | `2026-05-25T15:30:00` | `time.AfterFunc(delay)` |
| `every` | 固定间隔 | `3600` 秒 | `@every 1h0m0s` |
| `cron` | Cron 表达式 | `0 9 * * *` | 6段 cron 表达式 |

### CronPayload

```go
type CronPayload struct {
    Kind    string // "agent_turn" — 作为 Agent 消息发送
    Message string // 触发消息内容
    Deliver bool   // true=直接投递到 outbound, false=投递到 inbound 让 Agent 处理
    Channel string // 目标通道
    To      string // 目标会话
}
```

### CronService 结构

```go
type CronService struct {
    storePath string
    onJob     func(ctx context.Context, job *CronJob) error  // 回调
    cron      *cron.Cron               // robfig/cron 实例
    jobs      map[string]*CronJob      // 所有任务
    entryIDs  map[string]cron.EntryID  // cron 任务 ID 映射
    timers    map[string]*time.Timer   // 一次性定时器
    mu        sync.RWMutex
}
```

### 生命周期

```
Start(ctx)
├── loadStore()       ← 从 jobs.json 加载已持久化的任务
│   ├── 跳过过期的 at 任务 (超过当前时间的)
│   └── scheduleJob() 逐一调度
└── s.cron.Start()    ← 启动 robfig/cron

Stop()
├── s.cron.Stop()
└── 取消所有一次性 Timer
```

### executeJob — 任务执行

```go
func (s *CronService) executeJob(job *CronJob)
```

1. 调用 `s.onJob(ctx, job)` 回调
2. 更新 `State.LastRunAtMs`, `State.LastStatus`, `State.LastError`
3. 如果 `DeleteAfterRun == true` → 删除任务
4. `saveStore()` → 持久化到磁盘

### 持久化 (jobs.json)

```json
{
  "version": 1,
  "jobs": [
    {
      "id": "a1b2c3d4",
      "name": "每日早报",
      "enabled": true,
      "schedule": { "kind": "cron", "expr": "0 9 * * *", "tz": "Asia/Shanghai" },
      "payload": { "kind": "agent_turn", "message": "发送今日早报", "deliver": false },
      "state": { "lastRunAtMs": 0, "lastStatus": "", "lastError": "" },
      "createdAtMs": 1716614400000,
      "deleteAfterRun": false
    }
  ]
}
```

### API 方法

| 方法 | 说明 |
|------|------|
| `AddJob(name, schedule, payload, deleteAfterRun)` | 添加任务, 返回 *CronJob |
| `ListJobs()` | 返回排序后的任务列表（副本） |
| `RemoveJob(id)` | 删除任务 → true/false |

---

## 2. BuildCronJobHandler (app/cron.go)

将 CronService 连接到 MessageBus：

```go
func BuildCronJobHandler(messageBus, opts) func(ctx, job) error
```

配置项：

```go
type CronDispatchOptions struct {
    RequireChannel         bool  // 要求 channel 不能为空
    RequireNonEmptyMessage bool  // 要求消息不能为空
    EnableDeliver          bool  // 允许直接投递(Deliver模式)
}
```

**两种投递模式**：

```
job.Payload.Deliver == true:
    → PublishOutbound (直接发送给用户, 不经过 Agent)

job.Payload.Deliver == false:
    → PublishInbound (作为 Agent 消息入站, 触发 Agent 处理)
```

**Gateway 模式** (`nanobot gateway`): EnableDeliver=true, 支持两种模式
**CLI 模式**: 自定义 handler，只打印日志不实际执行

---

## 3. HeartbeatService (pkg/heartbeat/service.go)

### 设计目的

周期性让 LLM 检查 `HEARTBEAT.md` 文件，判断是否有待办任务需要提醒用户。

### HeartbeatService 结构

```go
type HeartbeatService struct {
    heartbeatPath string          // HEARTBEAT.md 路径
    model         model.ChatModel // LLM 实例
    onExecute     func(ctx, tasks) error  // 发现任务时的回调
    interval      time.Duration   // 检查间隔 (默认30分钟)
    stopChan      chan struct{}
}
```

### Tick — 单次心跳检查

```
1. os.ReadFile(heartbeatPath)  ← 读取 HEARTBEAT.md
   ├── 文件不存在或为空 → 跳过
   └── 有内容 → 继续

2. decide(content)  ← 让 LLM 决定
   ├── 构建 prompt: "你是一个心跳代理, 调用 heartbeat 工具..."
   ├── model.Generate(tool_choice=heartbeat)
   └── 解析工具调用结果

3. LLM 返回:
   ├── action="skip" → 没有任务, 跳过
   └── action="run"  → 有任务
       └── onExecute(ctx, tasks) → 转化为 InboundMessage
```

### decide — LLM 决策

```go
func (s *HeartbeatService) decide(ctx, content) (HeartbeatAction, string, error)
```

通过定义一个 `heartbeat` 工具让 LLM 调用：

```json
{
  "name": "heartbeat",
  "parameters": {
    "action": { "type": "string", "enum": ["skip", "run"] },
    "tasks":  { "type": "string", "description": "自然语言任务描述" }
  }
}
```

### StartHeartbeatService (app/heartbeat.go)

组装 HeartbeatService 连接到 MessageBus：

```go
func StartHeartbeatService(ctx, cfg, chatModel, messageBus) *HeartbeatService
```

`onExecute` 回调：将 LLM 识别出的任务发布为 InboundMessage：
```go
messageBus.PublishInbound(ctx, &InboundMessage{
    Channel:  "heartbeat",
    ChatID:   "system",
    Content:  tasks,          // LLM 返回的任务描述
    Metadata: map[string]any{"type": "heartbeat"},
})
```

这个 InboundMessage 会被 RunInboundLoop 处理，触发 Agent 执行任务。

---

## 4. 任务调度完整链路

### Cron 任务链路

```
用户: "每天早上9点提醒我开会"
    │
    ▼
Agent 调用 cron 工具: action=add
    │
    ▼
CronService.AddJob(...) → 注册 + 调度 + 持久化
    │
    ...时间到了...
    │
    ▼
robfig/cron 触发 → executeJob → onJob回调
    │
    ├── Deliver模式 → PublishOutbound (直接发消息)
    └── 非Deliver → PublishInbound (让Agent处理)
```

### Heartbeat 链路

```
HeartbeatService.Tick()  (每30分钟)
    │
    ▼
读取 HEARTBEAT.md
    │
    ▼
LLM 评估: skip or run?
    │
    └── run → PublishInbound("heartbeat", tasks)
              │
              ▼
          Agent 处理任务 → 回复用户
```
