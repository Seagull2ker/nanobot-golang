# 入口点 (cmd/)

nanobot-eino 提供了三个入口点，对应不同的使用场景。

## 文件结构

```
cmd/
├── cli/main.go              # 极简 CLI 模式
├── nanobot/                 # 全功能 CLI 工具 (cobra)
│   ├── main.go              # 根命令 + 子命令注册
│   ├── agent.go             # `nanobot agent` 交互式/单次对话
│   ├── gateway.go           # `nanobot gateway` 完整后端服务
│   ├── onboard.go           # `nanobot onboard` 初始化配置
│   └── status.go            # `nanobot status` 查看配置状态
└── server/main.go           # 纯后台 Gateway 服务（无 CLI，用环境变量配置）
```

---

## 1. cmd/nanobot/main.go — 根命令入口

使用 [cobra](https://github.com/spf13/cobra) 框架组织命令：

```go
rootCmd.AddCommand(
    newGatewayCmd(),   // nanobot gateway
    newAgentCmd(),     // nanobot agent
    newOnboardCmd(),   // nanobot onboard
    newStatusCmd(),    // nanobot status
    newVersionCmd(),   // nanobot version
)
```

关键函数 `mustLoadConfig()`:
- 调用 `config.Load(cfgFile)` 从 `~/.nanobot-eino/config.yaml` 加载配置
- 加载失败直接 `os.Exit(1)`

---

## 2. cmd/nanobot/agent.go — 交互式对话

### 子命令: `nanobot agent`

支持两种模式：

| 模式 | 用法 | 说明 |
|------|------|------|
| 单次消息 | `nanobot agent -m "hello"` | 发送一条消息后退出 |
| 交互式 | `nanobot agent` | 进入 REPL 对话循环 |

### `runAgent()` 初始化流程

```
1. 加载 Config
2. 初始化 Tracing (Langfuse)
3. 同步 Prompt 模板 (workspace.SyncTemplates)
4. 创建 MemoryStore → 磁盘存储记忆
5. 创建 SessionManager → 磁盘存储会话
6. BuildModelConfig → 解析 LLM 提供商和模型
7. buildCLIToolConfig → 构建工具配置
8. 创建 CronService
9. 创建 Agent (agent.NewAgent)
10. 根据模式进入 handleSingleMessage 或 runInteractive
```

### `runInteractive()` — 交互式 REPL

- 使用 `liner` 库支持行编辑和历史
- 历史文件路径: `~/.nanobot-eino/history/cli_history`
- 显示 `thinking...` 旋转动画（spinner）
- 特殊命令: `exit` 退出

### `handleSingleMessage()` — 单次消息

- 收集完流式响应后直接打印并退出
- 支持 `--raw` 选项关闭 Markdown 渲染

### `buildCLIToolConfig()`

与 Gateway 模式的工具配置不同：CLI 模式下工具的 `DefaultChannel/DefaultChatID` 从 sessionID 解析（如 `cli:user-1`）

---

## 3. cmd/nanobot/gateway.go — 完整 Gateway 服务

### 子命令: `nanobot gateway`

启动完整的后端服务，包含所有组件：

```go
// 初始化顺序
1. 加载 Config
2. 初始化 Tracing
3. 同步 Prompt 模板
4. 创建 MemoryStore
5. 创建 SessionManager
6. 创建 MessageBus（消息总线）★
7. BuildModelConfig → 创建 ChatModel
8. 创建 CronService
9. 创建 HeartbeatService
10. 创建 SubagentManager
11. 创建 Agent
12. 绑定进度回调 (OnProgress)
13. 启动飞书通道
14. 启动优雅关闭监听
15. RunInboundLoop → 主循环
```

### 与 server/main.go 的区别

`cmd/server/main.go` 和 `gateway.go` 功能几乎相同，区别在于：

| 特性 | cmd/server/ | cmd/nanobot gateway |
|------|-------------|---------------------|
| 配置来源 | 环境变量 `NANOBOT_CONFIG` | CLI `--config` 或默认路径 |
| 启动方式 | `go run ./cmd/server` | `nanobot gateway` |
| 命令行支持 | 无 | 有（cobra 子命令） |

---

## 4. cmd/nanobot/onboard.go — 初始化向导

### 子命令: `nanobot onboard`

创建默认配置和运行时目录：

```
~/.nanobot-eino/
├── config.yaml      # 默认配置文件
├── prompts/         # 系统提示词模板（SOUL.md 等）
├── skills/          # 内置技能目录
├── workspace/       # Agent 工作空间
├── sessions/        # 会话存储
├── memory/          # 记忆存储（MEMORY.md, HISTORY.md）
├── cron/            # 定时任务持久化
└── logs/            # 日志目录
```

执行后提示用户编辑 `config.yaml` 配置 API Key。

---

## 5. cmd/nanobot/status.go — 状态查看

### 子命令: `nanobot status`

打印当前配置摘要：
- 模型配置（Provider, Model, Base URL, API Key 掩码）
- Agent 配置（Prompt Dir, Context Window, Max Steps）
- 工具配置（Workspace, Search Provider, MCP）
- 通道配置（Feishu 是否配置）
- 路径信息（Config, Data, Sessions, Memory）

---

## 6. cmd/cli/main.go — 极简 CLI

最简单的入口，用于快速测试：

```
特点：
- 无命令行框架，直接 os.Stdin 读取
- 无 MessageBus，直接调用 bot.ChatStream
- 无子命令，只有一个对话循环
- sessionID 固定为 "cli:user-1"
```

### 与 nanobot agent 的区别

| 特性 | cmd/cli | nanobot agent |
|------|---------|---------------|
| 行编辑/历史 | 无 | 有 (liner) |
| Markdown 渲染 | 无 | 有 (glamour) |
| 单次消息模式 | 无 | 有 (-m 参数) |
| 代码量 | ~120 行 | ~280 行 |
