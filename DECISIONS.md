# DECISIONS.md

重要架构决策记录。每个决策包含背景、选项、决定和理由。

Last updated: 2026-05-23

---

## D1: 使用 Eino ChatModelAgent 而非手写 ReAct 循环

**日期：** 2026-05-22

**背景：** Python 版 AgentRunner 手写了完整的 ReAct 循环（think → act → observe，~500 行）。Go 版需要决定是否复刻手写循环还是使用框架。

**选项：**
- A: 手写 ReAct 循环（完全控制，但工作量大）
- B: 使用 Eino ChatModelAgent（内置 ReAct Graph，通过中间件扩展）

**决定：** 选 B。Eino 内置 ReAct Graph + 3 个自定义 middleware（Governance / ToolSafety / Thinking）+ AgentLoop 层处理 injection/empty/length recovery。

**理由：** Eino 已处理 CheckPoint/Resume、流式管道、子 Agent 路由、模型重试。手写这些需要额外 2000+ 行代码且容易出 bug。Governance（orphan/backfill/microcompact/snip）通过 BeforeModelRewriteState 中间件实现，功能与 Python 版等价。

---

## D2: 保留 Provider Registry + Factory 模式

**日期：** 2026-05-23

**背景：** 初始方案曾考虑简化 provider 创建（直接 switch-case），用户明确纠正必须保留 registry + factory。

**决定：** `BackendType` 枚举驱动（openai_compat / anthropic），registry 注册 ProviderSpec，factory 根据 BackendType 创建对应适配器实例。

**理由：** registry 支持按模型名自动匹配 provider，factory 封装 API key 注入、重试包装、断路器。新增 provider 只需注册 ProviderSpec + 实现 ChatModelAdapter 接口。

---

## D3: 首期仅支持 4 个 Provider

**日期：** 2026-05-22

**背景：** Python 版 registry 有 ~50 个 provider 注册项。Go 版需要决定 scope。

**选项：**
- A: 全量迁移 ~50 个 provider
- B: 先 4 个主流 provider，架构预留扩展

**决定：** 选 B。OpenAI、Anthropic、DeepSeek、DashScope。其中 OpenAI/DeepSeek/DashScope 共用 openai_compat 适配器。

**理由：** 4 个 provider 覆盖 80%+ 使用场景。BackendType 模式使新增 provider 只需注册 spec + 少量差异化配置，无需新适配器代码。

---

## D4: 首期仅支持飞书 + WebSocket Channel

**日期：** 2026-05-22

**背景：** Python 版支持 15+ 平台。Go 版需确定优先级。

**决定：** Phase 4 只做飞书 + WebSocket（WebUI 通信）。其余平台后续按需添加。

**理由：** 飞书是主要使用平台，WebSocket 是 WebUI 必需。Channel 接口设计支持后续扩展，无需改动核心架构。

---

## D5: 放弃 Channel/Tool 自动发现，使用显式注册

**日期：** 2026-05-22

**背景：** Python 版通过 `pkgutil.walk_packages` 自动扫描和加载 channel 和 tool 插件。Go 没有等效机制。

**决定：** Tool 通过 `init()` 自注册到全局 Registry；Channel 在 manager 中显式注册。

**理由：** Go 的编译时安全优于 Python 的运行时灵活性。显式注册更清晰、可调试。插件机制可通过 Go plugin 或外部进程后续添加。

---

## D6: 保留 Dream 两阶段记忆处理

**日期：** 2026-05-23

**背景：** 初始方案曾将 Dream 标记为"暂不实现"，用户明确纠正必须保留。

**决定：** Phase 3 实现完整 Dream：Phase 1 LLM 分析未处理事件 → 结构化观察报告 → Phase 2 AgentRunner 执行 MEMORY.md 编辑（含 go-git blame 行龄标注）。

**理由：** Dream 是 nanobot 记忆系统的核心差异化功能，不可省略。

---

## D7: 保留 SubAgent + MCP 工具

**日期：** 2026-05-23

**背景：** 用户明确要求保留子 Agent 和 MCP 工具。

**决定：**
- SubAgent：使用 Eino 内置的 `OnSetSubAgents` + `transfer_to_agent` 工具 + `flowAgent` 路由
- MCP：mark3labs/mcp-go 实现 MCP 客户端和服务发现

**理由：** Eino 已提供子 Agent 基础设施，比 Python 手写 SubagentManager 更简洁。MCP 是工具扩展的关键机制。

---

## D8: 使用 Viper 管理配置

**日期：** 2026-05-23

**背景：** Phase 0 实现时先用 `encoding/json` 完成了 loader。后续用户选择 Viper。

**决定：** plan.md 已更新为 Viper 方案（JSON 文件 + NANOBOT_ 环境变量自动绑定 + pflag 集成）。**代码尚未迁移。**

**状态：** ✅ 已迁移（2026-05-24）。Load() 使用 viper 读取 JSON，应用 `viper.DecoderConfigOption` 以 `json` tag 做 mapstructure 映射。`applyEnvOverrides` 保留手动 NANOBOT_ 环境变量覆盖（兼容双下划线分隔约定）。Save() 保留 encoding/json。

---

## D9: 三个 Eino 中间件 vs 修改 Eino 源码

**日期：** 2026-05-22

**背景：** Python AgentRunner governance 逻辑（orphan cleanup、backfill、microcompact、snip）在 Eino 中没有直接对应。需决定扩展方式。

**选项：**
- A: Fork Eino 并修改 ChatModelAgent 内部逻辑
- B: 通过 ChatModelAgentMiddleware 接口实现 3 个自定义中间件

**决定：** 选 B。Governance 中间件（BeforeModelRewriteState）+ ToolSafety 中间件（WrapInvokableToolCall）+ Thinking 中间件（WrapModel）。

**理由：** 不 fork 框架，升级不受影响。中间件接口足够覆盖所有 governance 需求。AgentLoop 层补充处理 injection/empty/length recovery（中间件做不到的部分）。

---

## D10: 工具 Sandbox 仅本地模式

**日期：** 2026-05-22

**背景：** Python 版有 LocalSandboxBackend 和 DockerSandboxBackend。

**决定：** 只实现 LocalSandboxBackend（os/exec + context timeout）。Docker sandbox 暂不实现。

**理由：** Docker sandbox 增加运维复杂度，大部分场景本地沙箱足够。接口预留，后续可按需添加。

---

## D11: 图像生成 / 语音转写暂不实现

**日期：** 2026-05-22

**决定：** 非核心功能，后续按需添加。不影响主体架构。

---

## D12: Go 版本

**日期：** 2026-05-22

**背景：** plan.md 写 Go 1.22，实际 go.mod 为 Go 1.23。

**决定：** 以 go.mod 为准（Go 1.23.0）。

---

## D13: JSON camelCase 兼容

**日期：** 2026-05-22

**决定：** Config struct 使用 `json` tag 同时支持 snake_case 和 camelCase，保持与 Python 后端 config.json 的互操作性。已验证（config_test.go TestJSONCamelCaseCompat）。
