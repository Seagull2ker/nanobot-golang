# 记忆与会话管理 (pkg/memory/ + pkg/session/)

## 概述

记忆系统采用**双文件持久化**方案，会话系统采用 **JSONL 磁盘文件 + 内存缓存** 方案。两者配合实现对话历史的持久化、自动裁剪和长期记忆提取。

## 文件结构

```
pkg/memory/
├── memory.go       # MemoryStore — 双文件持久化记忆存储
└── consolidator.go # MemoryConsolidator — Token 预算管理和整合策略

pkg/session/
└── manager.go      # Session / SessionManager — 会话生命周期管理
```

---

## 1. Session 会话 (pkg/session/manager.go)

### Session 结构体

```go
type Session struct {
    Key              string            // 会话标识，如 "feishu:oc_xxx"
    Messages         []*schema.Message // 所有消息（包括 tool_calls 和 tool results）
    CreatedAt        time.Time
    UpdatedAt        time.Time
    Metadata         map[string]any
    LastConsolidated int               // 已被整合的消息索引边界
}
```

`LastConsolidated` 是关键字段：它之前的消息已被 LLM 总结并存入了长期记忆（MEMORY.md），之后的才是"活跃"的短期记忆。

### GetHistory — 获取历史消息

```go
func (s *Session) GetHistory(maxMessages int) []*schema.Message
```

1. 从 `LastConsolidated` 开始取未整合的消息
2. 如果 `maxMessages > 0`，只取最近 N 条
3. **丢弃开头的非 User 消息**：避免孤立的 tool_result 块出现在上下文开头

### SessionManager — 会话管理器

```go
type SessionManager struct {
    sessionsDir string             // ~/.nanobot-eino/sessions/
    cache       map[string]*Session // 内存缓存
    mu          sync.RWMutex
}
```

### 磁盘格式 (JSONL)

每个会话存为一个 `.jsonl` 文件（文件名 = session key，`:` 替换为 `_`）：

```
{"_type":"metadata","key":"feishu_oc_xxx","created_at":"...","updated_at":"...","last_consolidated":5, ...}
{"role":"user","content":"你好"}
{"role":"assistant","content":"你好！有什么可以帮你的？"}
{"role":"user","content":"帮我查天气"}
{"role":"assistant","tool_calls":[{"function":{"name":"web_search","arguments":"..."}}]}
{"role":"tool","content":"...搜索结果..."}
{"role":"assistant","content":"今天的天气是..."}
```

第一行是元数据行（`_type: "metadata"`），后续每行是一条消息。

### 关键方法

| 方法 | 说明 |
|------|------|
| `GetOrCreate(key)` | 先从缓存取 → 从磁盘加载 → 创建新会话 |
| `Save(s)` | 原子写 JSONL 文件，更新缓存 |
| `Invalidate(key)` | 从缓存删除，下次 GetOrCreate 会重新从磁盘加载 |
| `ListSessions()` | 列出所有会话文件名 |

---

## 2. MemoryStore — 记忆存储 (memory.go)

### 双文件设计

```
~/.nanobot-eino/memory/
├── MEMORY.md    # 长期记忆 — 覆盖写
└── HISTORY.md   # 对话历史日志 — 追加写
```

| 文件 | 写入方式 | 内容 | 用途 |
|------|----------|------|------|
| MEMORY.md | 覆盖 | 结构化的长期记忆（Markdown） | 注入到每次对话的 System Prompt |
| HISTORY.md | 追加 | 每条整合记录（带时间戳） | Grep 搜索历史事件 |

### GetMemoryContext — 获取记忆上下文

```go
func (s *MemoryStore) GetMemoryContext() string {
    longTerm := s.ReadLongTerm()
    if longTerm == "" { return "" }
    return "## Long-term Memory\n" + longTerm
}
```

如果 MEMORY.md 为空，不注入任何内容；否则以 `## Long-term Memory` 标题注入到 System Prompt。

### Consolidate — LLM 驱动的记忆整合

```go
func (s *MemoryStore) Consolidate(ctx, messages, chatModel) bool
```

这是记忆系统的核心方法：

```
1. 读取当前 MEMORY.md 内容（可能为空）
2. 构建提示词: "处理以下对话，调用 save_memory 工具"
3. 创建 save_memory 工具（工具定义，参数是 SaveMemoryArgs）
4. 让 LLM 调用 save_memory 工具
   ├── 尝试 tool_choice=forced（强制调用工具）
   └── 如果模型不支持 → 降级 tool_choice=allowed
5. 解析 LLM 的工具调用结果:
   ├── history_entry → 追加到 HISTORY.md（自动补时间戳）
   └── memory_update → 覆盖 MEMORY.md（如果内容有变化）
```

### SaveMemoryArgs — LLM 整合输出格式

```go
type SaveMemoryArgs struct {
    HistoryEntry string // 一段摘要段落，以 [YYYY-MM-DD HH:MM] 开头
    MemoryUpdate string // 更新后的完整长期记忆（Markdown）
}
```

### failOrRawArchive — 降级机制

如果 LLM 整合连续失败 3 次：
1. 将原始消息直接追加到 HISTORY.md
2. 标记为 `[RAW ARCHIVE]`
3. 重置失败计数

这确保即使 LLM 不可用，对话历史也不会丢失。

---

## 3. MemoryConsolidator — 记忆整合器 (consolidator.go)

`MemoryConsolidator` 管理整合策略：**何时整合、整合哪部分**。

```go
type MemoryConsolidator struct {
    Store               *MemoryStore
    chatModel           emodel.ChatModel
    sessions            *session.SessionManager
    contextWindowTokens int    // 模型上下文窗口大小
    basePromptTokens    int    // 系统提示词估算 token 数
    locks               sync.Map // 每个 session 一把锁，防止并发整合
}
```

### Token 估算策略

由于 Go 版本不依赖 tokenizer，使用简单的**字符数/3**估算：

```go
func estimateMessageTokens(msg *schema.Message) int {
    chars := len(msg.Content)
    for _, tc := range msg.ToolCalls {
        chars += len(tc.Function.Name) + len(tc.Function.Arguments)
    }
    return max(1, chars/3)
}
```

### PickConsolidationBoundary — 选择整合边界

```go
func (c *MemoryConsolidator) PickConsolidationBoundary(s *Session, tokensToRemove int) (
    endIdx int, removedTokens int, found bool)
```

**核心规则**：只在 User 消息边界处分割。这是因为：
- 不能在 Assistant 工具调用中间切断（会导致 orphaned tool results）
- User 消息是自然的"轮次"边界

算法：
1. 从 `LastConsolidated` 开始遍历
2. 累计 token 数
3. 遇到 User 消息时检查是否已达到需要删除的 token 数
4. 返回最近的 User 消息边界

### MaybeConsolidateByTokens — 按 Token 整合

```go
func (c *MemoryConsolidator) MaybeConsolidateByTokens(ctx, s *Session)
```

**触发条件**: 估算的 prompt token 数 > 上下文窗口的 **50%**

**执行流程**:
```
1. 加锁 (per-session)
2. 估算当前 prompt tokens
3. 如果 < target(窗口50%) → 跳过
4. 循环 (最多5轮):
   a. PickConsolidationBoundary → 找到可安全删除的消息块
   b. ConsolidateMessages(chunk) → LLM 整合
   c. 更新 s.LastConsolidated
   d. 保存会话
   e. 重新估算 → 如果 < target 则退出
```

### ArchiveUnconsolidated — 归档全部未整合消息

在 `/new` 命令时调用：
- 将 `[LastConsolidated:]` 的所有消息一次性全部整合
- 如果整合失败返回 error（阻止清空会话）

---

## 完整的内存生命周期

```
用户发消息
    │
    ▼
MaybeConsolidateByTokens (预整合)
    │  如果上下文超过窗口50%: 自动裁剪旧消息到 MEMORY.md
    ▼
Agent 处理消息...
    │
    ▼
保存消息到 Session
    │
    ▼
MaybeConsolidateByTokens (后整合)
    │  再次检查是否需要裁剪
    ▼
────────────────────────────────
用户发 /new 命令
    │
    ▼
ArchiveUnconsolidated
    │  将当前会话剩余全部整合到 MEMORY.md
    ▼
sess.Clear()  → 清空消息，重置 LastConsolidated
```
