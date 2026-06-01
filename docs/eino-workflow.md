# Eino 业务流程约束

本项目后端必须优先使用 CloudWeGo Eino 官方抽象组织智能体业务。KKG HTTP API 只作为工具实现细节存在，不能绕过 Eino 编排直接散落调用。

## 官方分层对应

```text
HTTP Router
  -> agent.Service
    -> Eino Chain / Graph
      -> normalize
      -> rag_retrieve
      -> prepare_session
      -> router ChatModelAgent
         -> direct answer
         -> AgentTool(platform_agent)
         -> AgentTool(blog_agent)
         -> AgentTool(question_agent)
            -> ChatModel ReAct decision
            -> compose.ToolsNode
               -> tool.BaseTool / tool.InvokableTool
                  -> internal/kkg HTTP Client
      -> persist_session
      -> build_response
```

- Component：模型、Retriever、Tool 等可替换组件。当前 RAG 通过 `rag.Retriever` 保留组件边界。
- Tool：所有 KKG 能力必须包装为 `tool.BaseTool`。涉及结构化业务结果时，优先使用 Eino `EnhancedInvokableTool` / `schema.ToolResult`，而不是把异构 JSON 字符串散落到业务层。
- Orchestration：确定性外围流程使用 Chain/Graph；模型决策、工具选择和工具观察必须交给 Eino ADK `ChatModelAgent`。
- Agent：智能体核心使用 `adk.NewChatModelAgent` 与 `adk.NewRunner`。当前项目采用一个顶层 router agent + 多个专项 sub-agent，并通过 `adk.NewAgentTool(...)` 做官方推荐的 agent collaboration。

## 当前严格边界

- `internal/kkgtools` 只负责定义 Eino Tool 输入结构、描述和 Invokable 函数。
- `internal/agent` 负责 Chain/Graph 外围编排、RAG 注入、ADK Runner 调用和会话消息管理。
- `/api/v1/tools` 返回 Eino `schema.ToolInfo`，前端展示的工具 schema 以 Eino 为准。
- `internal/kkg` 只负责 HTTP、鉴权 token/cookie 透传和 KKG 响应解析，不承载智能体决策。

## 第一条业务流：定向询问题目做法

当用户提供 `question_id` 并询问题目做法时，Graph 必须按以下流程运行：

```text
normalize
  -> rag_retrieve
  -> prepare_session
  -> router agent
     -> if request is generic: answer directly
     -> if request is platform-related: call platform_agent as AgentTool
     -> if request is blog-related: call blog_agent as AgentTool
     -> if request is question-related: call question_agent as AgentTool
        -> kkg_question_agent decides KKG tool calls
        -> ToolsNode executes KKG tools
        -> kkg_question_agent observes tool results
        -> final Markdown answer
  -> persist_session
  -> build_response
```

非 KKG/OJ 题目类请求必须由 router agent 决定是否直接回答或委派给相应 sub-agent，不能再依赖关键词硬编码路由。

当前已接入 DeepSeek ChatModel。没有配置 `DEEPSEEK_API_KEY` 时，服务启动阶段应失败或拒绝构建 Agent，不能伪装成已经具备模型驱动智能体能力。

DeepSeek 配置只允许放在本地 `.env` 或部署密钥系统中，不能写入源码、文档或 `.env.example` 的真实值。

## 会话上下文

`RunRequest.session_id` 是多轮上下文键。服务端通过 `internal/memory.Store` 在业务层统一管理：

- 会话消息：按 `schema.Message` 持久化最近若干轮用户/助手消息。
- Runner checkpoint：作为 Eino ADK `CheckPointStore` 注入 `adk.Runner`。

下一轮请求会先从 `memory.Store` 读取历史消息，再与当前 RAG 注入后的用户消息一起传给 `adk.Runner`。这样多轮上下文与 ADK checkpoint 都由业务层存储承接，而不是散落在 `agent.Service` 的临时内存里。

运行时的跨 agent / 跨 tool 共享上下文，例如 `access_token`、`user_id`、`request_id`，通过 `adk.WithSessionValues(...)` 注入，并由工具侧使用 `adk.GetSessionValue(...)` 读取。不要再用项目私有的 `context.WithValue` 传递业务关键信息。

当前默认实现使用 Postgres 表：

- `agent_sessions`
- `agent_messages`
- `agent_checkpoints`

只有在显式不给 `POSTGRES_DSN` 时，才允许退化为进程内存存储用于本地调试。

## 工具调用原则

```text
ChatModelAgent
  -> model.WithTools(tool infos)
  -> assistant tool calls
  -> ToolsNode
  -> tool messages / ToolResult
  -> final answer
```

工具选择必须由 `ChatModelAgent` 的 ReAct loop 产生；业务代码只能提供工具集合、上下文和约束提示，不能预先硬编码“必须调用哪些工具”作为主流程。

对多智能体协作还必须额外遵守：

- 顶层 agent 通过 `AgentTool` 委派 sub-agent，不再在业务代码里用关键词 `switch runner`。
- sub-agent 默认不继承父对话全量历史；需要通过 `AgentTool` schema 显式传递自包含请求。
- `question_agent` 这类需要结构化字段的 sub-agent，必须通过 `WithAgentInputSchema(...)` 定义输入边界，而不是依赖模糊自由文本。

工具返回必须保持统一边界：

- 对模型侧：通过 Eino `schema.ToolResult` 传递标准化工具观察结果。
- 对业务侧：服务层统一解析 Tool message，不按具体工具名分支硬编码主逻辑。
- 对记忆侧：会话存储保留本轮 user / assistant / tool 全量消息，而不是只保存最终答案。

## 工具开发规则

新增 KKG 工具时按以下顺序处理：

1. 在 `internal/kkg` 增加 API client 方法，只做 HTTP 契约适配。
2. 在 `internal/kkgtools` 增加输入结构和 `utils.InferTool` 定义。
3. 在 `kkgtools.New` 注册工具。
4. 更新 Agent instruction，让模型知道何时使用新工具。
5. 通过 `/api/v1/tools` 校验 schema，通过 `/api/v1/agent/run` 校验 ADK 事件轨迹。

禁止在 `agent.Service` 中直接调用 `kkg.Client` 完成工具业务。
