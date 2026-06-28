# Backend Structure

后端代码按运行边界拆分，而不是按技术名词堆在单个文件里。

## apps/api/internal/agent

智能体服务层，负责 Eino Graph、ADK Runner、会话消息和前端响应。

- `service.go`: `Service` 构造、公开入口、Graph 节点实现和响应汇总。
- `agent_topology.go`: 顶层 router agent、子 agent、AgentTool 和工具集合组装。
- `workflow.go`: Eino chain / graph 的节点和边定义。
- `adk_runtime.go`: ADK 事件消费、流式消息合并、工具结果解析、interrupt / resume 数据解析。
- `request_context.go`: 请求归一化辅助、题号/提交 ID 抽取、上下文继承、代码协议清洗。
- `prompts.go`: router / sub-agent 指令和 AgentTool 输入 schema。
- `sessions.go`: 会话列表、会话加载、历史消息清理。
- `observability.go`: Eino callback trace、流式事件和 token usage 汇总。
- `types.go`: API DTO、运行态 state、trace/result 类型。

## apps/api/internal/kkgtools

KKG API 的 Eino Tool 包装层。这里不做智能体编排，只定义工具输入、执行和标准化结果。

- `tools.go`: 工具注册和每个工具的 `InferEnhancedTool` 实现。
- `types.go`: Tool 输入结构和提交 interrupt 状态结构。
- `context.go`: 从 ADK session values 读取登录态和用户信息。
- `result.go`: 统一的工具结果 payload 和错误结构。
- `helpers.go`: 分页限制、摘要文本、提交结果摘要等局部 helper。

## Boundary Rules

- `agent` 可以组装工具和调用 ADK Runner，但不直接访问 KKG HTTP API。
- `kkgtools` 可以调用 `kkg.Client`，但不持有会话、不做 router 决策、不生成最终用户答案。
- `kkg.Client` 只负责 HTTP 契约和响应归一化，不依赖 Eino / ADK。
- 新增工具时优先改 `kkgtools/types.go` 和 `kkgtools/tools.go`，再到 `agent_topology.go` 挂到对应子智能体。
