# Agent 核心 (pkg/agent/)

## 概述

Agent 是 nanobot-eino 的核心，负责接收用户消息、构建提示词、调用 LLM 进行 ReAct 循环、处理工具调用、返回流式响应。

## agent.go 结构

文件位置: `pkg/agent/agent.go` (~615 行)

---

## 1. Agent 结构体

```go
type Agent struct {
    mu              sync.RWMutex
    reactAgent      *react.Agent          // Eino React Agent 实例
    consolidator    *memory.MemoryConsolidator  // 记忆整合器
    promptLoader    *prompt.Loader        // 提示词加载器
    cronService     *cron.CronService     // 定时任务服务
    skillManager    *skill.Manager        // 技能管理器
    sessions        *session.SessionManager // 会话管理器
    toolCfg         tools.ToolConfig      // 工具配置

    activeTasks     sync.Map              // map[string]context.CancelFunc (/stop 支持)
    subagentManager *subagent.SubagentManager

    OnProgress      tools.ToolProgressFunc // 工具进度回调

    mcpConfigs      []tools.MCPServerConfig // MCP 配置(延迟加载)
    mcpOnce         sync.Once
    baseTools       []tool.BaseTool

    toolCallingModel emodel.ToolCallingChatModel // 支持原生 Tool Calling 的模型
    chatModel        emodel.ChatModel            // 普通 Chat 模型
    maxStep          int
}
```

---

## 2. NewAgent — 初始化流程

```
NewAgent(ctx, modelCfg, toolCfg, memStore, promptDir, skillsDir, cronService, sessionMgr, contextWindowTokens, maxStep, subagentMgr)
```

### 初始化步骤

```
1. model.NewChatModel(ctx, modelCfg)
   └─ 根据 modelCfg.Type 创建对应的 LLM 客户端

2. skill.NewManager(workspace, builtinSkillsDir)
   └─ 扫描 workspace/skills 和内置 skills 目录
   └─ LoadSkills() 解析所有 SKILL.md 文件

3. 提取 MCP 配置（从 toolCfg.MCP 中拿出，后续延迟加载）
   └─ toolCfg.MCP = nil（后面的 NewTools 不再处理）

4. tools.NewTools(ctx, toolCfg)
   └─ 创建工具集: web_search, web_fetch, read_file, write_file,
                 edit_file, list_dir, message, shell_exec

5. 如果有 cronService → 注册 cron 工具
6. 如果有 subagentMgr → 注册 spawn 工具

7. prompt.NewLoader(promptDir)
   └─ 提示词加载器

8. memory.NewMemoryConsolidator(memStore, chatModel, sessionMgr, window, basePrompt)
   └─ 记忆整合器（负责自动裁剪旧对话）

9. tools.WrapTools(invokableTools, maxChars, progressFn)
   └─ 所有工具包装一层（截断 + 进度回调）

10. a.newReactAgent(ctx, baseTools)
    └─ 创建 Eino React Agent
```

### 工具调用模型检测

```go
if tcm, ok := chatModel.(emodel.ToolCallingChatModel); ok {
    a.toolCallingModel = tcm  // 原生 Tool Calling API
}
```

如果模型实现了 `ToolCallingChatModel` 接口，会优先使用原生 Tool Calling 模式（如 Claude 的 tool_use、OpenAI 的 function calling），否则使用 Eino 的通用 BindTools 模式。

---

## 3. ChatStream — 核心对话方法

```go
func (a *Agent) ChatStream(ctx context.Context, sessionID, input string) (
    *schema.StreamReader[*schema.Message], error)
```

### 3.1 特殊命令处理（同步返回）

| 命令 | 行为 |
|------|------|
| `/new` / `new` / `新会话` | 归档旧对话到记忆，清空会话，返回确认文本 |
| `/help` / `help` | 返回命令列表文本 |
| `/stop` | 取消当前 session 的活跃任务 |
| `/restart` | 通过 `syscall.Exec` 原地重启进程 |

### 3.2 正常对话流程（异步 goroutine）

```
ChatStream
├── ensureMCPConnected(ctx)          ← (首次) 延迟连接 MCP 服务器
├── taskCtx = context.WithCancel(ctx) ← 创建可取消的子 Context
├── ContextWithSessionID(taskCtx, sessionID)  ← 注入 sessionID 用于进度回调
├── sess = sessions.GetOrCreate(sessionID)    ← 获取/创建会话
├── consolidator.MaybeConsolidateByTokens()   ← 预整合: 如果上下文过长则裁剪
├── history = sess.GetHistory(0)              ← 获取未整合的消息历史
├── messages = buildMessages(ctx, history, input) ← 构建完整提示词
├── currentAgent.Stream(taskCtx, messages, WithMessageFuture())
│                                                ← Eino React Agent 循环
├── [goroutine]
│   ├── 并行收集 future 中的中间消息
│   ├── 消费 stream，拼接 fullResponse
│   ├── 将 stream 消息转发给 pipeWriter（用户侧读取）
│   ├── 等待中间消息收集完成
│   ├── 保存会话: sess.AddMessage(input) + 中间消息
│   ├── sessions.Save(sess)
│   └── consolidator.MaybeConsolidateByTokens() ← 后整合
└── return pipeReader  ← 用户通过 pipeReader 消费流式输出
```

### 3.3 流式双管道设计

`ChatStream` 返回后，调用方通过 `pipeReader.Recv()` 逐步读取 Agent 输出。内部使用 Eino 的 `schema.Pipe` 创建管道：

```
Eino Stream ──→ goroutine 消费 ──→ pipeWriter ──→ pipeReader ──→ 调用方
                   │
                   └──→ future.GetMessageStreams() (并行收集中间消息)
```

`WithMessageFuture` 让 Eino 在生成最终回复的同时，提供所有中间步骤的完整消息（包括 tool_calls 消息和 tool result 消息），用于完整保存会话历史。

---

## 4. buildMessages — 提示词构建

```go
func (a *Agent) buildMessages(ctx, history, input) ([]*schema.Message, error)
```

构建的消息序列为：

```
[System Message]
├── # SOUL          (来自 SOUL.md)
├── # USER PROFILE  (来自 USER.md)
├── # TOOL USAGE    (来自 TOOLS.md)
├── # AGENT INSTRUCTIONS (来自 AGENTS.md)
├── # HEARTBEAT TASKS   (如果 HEARTBEAT.md 存在)
├── ---
├── # Active Skills (always=true 的技能的完整正文)
├── ---
├── # Skills        (所有可用技能的 XML 摘要)
├── ---
├── ## Long-term Memory (来自 MEMORY.md)
│
├── [History Message 1]   (会话历史中未整合的消息)
├── [History Message 2]
├── ...
│
└── [User Message]
    ├── [Runtime Context]
    │   ├── Current Time: 2026-05-25 15:04 (Mon) CST
    │   ├── Channel: feishu      (如果有)
    │   └── Chat ID: oc_xxx       (如果有)
    └── <用户输入文本>
```

### 各 Section 来源

| Section | 文件/来源 | 说明 |
|---------|-----------|------|
| SOUL | `promptDir/SOUL.md` | Agent 人格定义 |
| USER PROFILE | `promptDir/USER.md` | 用户画像 |
| TOOL USAGE | `promptDir/TOOLS.md` | 工具使用说明 |
| AGENT INSTRUCTIONS | `promptDir/AGENTS.md` | Agent 行为指令 |
| HEARTBEAT TASKS | `promptDir/HEARTBEAT.md` | 定时检查任务（可选） |
| Active Skills | skillManager | always=true 的技能的 SKILL.md 内容 |
| Skills | skillManager | 所有技能的 XML 格式摘要 |
| Long-term Memory | MEMORY.md | 持久化的长期记忆 |
| Runtime Context | 运行时生成 | 当前时间、通道、聊天ID |

---

## 5. handleNewSession — /new 命令

完整流程：

```
1. 获取当前会话
2. consolidator.ArchiveUnconsolidated(ctx, sess)
   └─ 将未整合的消息全部送入 LLM 整合 → 存为 MEMORY.md
3. sess.Clear()
   └─ 清空消息列表，重置 LastConsolidated
4. sessions.Save(sess)
5. sessions.Invalidate(sessionID)
   └─ 从缓存中移除，下次重新从磁盘加载
6. 返回 "新会话开始，旧对话已归档到记忆"
```

---

## 6. handleStop / CancelAll — 任务取消

### /stop 命令

```go
func (a *Agent) handleStop(sessionID string)
```

1. 从 `activeTasks` sync.Map 中取出该 session 的 cancel 函数并调用
2. 同时取消该 session 的所有 subagent 子任务
3. 返回确认消息

### CancelAll（关机时调用）

遍历所有活跃任务，逐一 cancel。

---

## 7. handleRestart — 原地重启

```go
func (a *Agent) handleRestart()
```

通过 `syscall.Exec` 实现原地进程替换：

1. 发送 "Restarting..." 确认消息
2. `time.Sleep(500ms)` 等待消息发出
3. `os.Executable()` 获取当前二进制路径
4. `syscall.Exec(exe, os.Args, os.Environ())` 原地替换进程

---

## 8. ensureMCPConnected — MCP 延迟加载

```go
func (a *Agent) ensureMCPConnected(ctx context.Context)
```

使用 `sync.Once` 确保只执行一次：

1. 遍历 `a.mcpConfigs`，逐一 `ConnectMCPServer`
2. 将 MCP 工具包装（WrapTools）
3. 合并到现有工具列表
4. 重新创建 Eino React Agent（`a.newReactAgent(ctx, newBaseTools)`）
5. 原子替换 `a.reactAgent` 和 `a.baseTools`

---

## 9. newReactAgent — Agent 实例创建

```go
func (a *Agent) newReactAgent(ctx, allTools) (*react.Agent, error)
```

委托给 `reactutil.NewReactAgent`，传入：
- 工具列表
- `UnknownToolsHandler`: LLM 调用不存在的工具时返回友好错误
- `ToolArgumentsHandler`: 预处理工具参数 JSON（去 code fence、验证 JSON 格式）

---

## 关键辅助模块

| 模块 | 文件 | 作用 |
|------|------|------|
| Prompt Loader | `pkg/prompt/loader.go` | 读取 SOUL/USER/TOOLS/AGENTS/HEARTBEAT.md |
| Skill Manager | `pkg/skill/manager.go` | 加载和查询技能 |
| React Util | `pkg/reactutil/react.go` | 共享的 React Agent 创建逻辑 |
