# 架构说明

## 目标

当前项目用于服务 KKG，提供一个独立开发、独立部署的智能体系统：

- 前端：Nuxt + Vue，负责智能体运行台、工具调用轨迹、RAG 引用和后续任务管理。
- 后端：Go + CloudWeGo Eino，负责 chain/graph 编排、工具调用、RAG 检索、模型调用和审计。
- RAG：以 pgvector 作为本地开发基线，保留替换为 Milvus、Qdrant 或云向量库的接口边界。
- KKG 集成：不复制 KKG 服务逻辑，通过 HTTP API 与共享鉴权把 KKG 能力包装成工具。

## 目录

```text
apps/
  api/      Go Agent API，Eino chain/graph 编排
  web/      Nuxt + Vue 前端
docs/       设计与接口文档
infra/      本地依赖初始化脚本
scripts/    本地开发入口
```

## 后端分层

```text
cmd/agent-api        进程入口
internal/http        Gin 路由、CORS、请求鉴权透传
internal/agent       Eino chain 与 graph 编排
internal/kkg         KKG API 工具客户端
internal/rag         RAG 查询、文档和 Retriever 接口
internal/config      环境变量配置
internal/response    统一响应 envelope
```

## Eino 编排

当前已实现两条可运行编排：

- `chain`: `normalize -> rag_retrieve -> plan_tools -> execute_tools -> synthesize`
- `graph`: `START -> normalize -> rag_retrieve -> plan_tools -> execute_tools -> synthesize -> END`

`chain` 用于确定性线性流程，适合固定题解生成、固定摘要生成等任务。`graph` 用于后续扩展条件分支，例如：

- 判断是否有 `question_id`，再决定是否调用 OJ 题目工具。
- 判断 RAG 命中是否足够，再决定是否调用博客搜索工具。
- 判断用户角色是否为 admin，再决定是否调用管理型工具。
- 在失败节点上接入重试、降级和人工确认。

KKG 工具严格按 Eino Tool 流程处理：`internal/kkgtools` 使用 `utils.InferTool` 生成 `tool.BaseTool`，`plan_tools` 生成 `schema.ToolCall`，`execute_tools` 统一交给 `compose.ToolsNode` 执行。当前尚未接入真实 ChatModel，所以 `plan_tools` 是确定性规划节点；接入模型后该节点会替换为模型/ReAct 决策节点。

## API

Agent API 当前暴露：

- `GET /health`
- `POST /api/v1/agent/run`
- `GET /api/v1/tools`
- `POST /api/v1/auth/login`
- `GET /api/v1/auth/me`
- `POST /api/v1/auth/refresh`
- `POST /api/v1/auth/logout`

请求：

```json
{
  "mode": "graph",
  "query": "为 KKG OJ 题目生成题解思路",
  "question_id": 1001
}
```

响应沿用 KKG 风格：

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "mode": "graph",
    "answer": "...",
    "rag_docs": [],
    "tool_trace": []
  }
}
```

## 开发环境

开发容器通过 `docker-compose.dev.yml` 启动独立依赖：

- `workspace`: Go + Node 开发环境
- `postgres`: PostgreSQL 16 + pgvector
- `redis`: 任务状态、缓存、后续流式事件
- `minio`: 文档、图片、附件原始对象

KKG 主项目服务不放进本 compose。通过 `.env` 指向外部服务：

```env
KKG_BLOG_BASE_URL=http://host.docker.internal:8080
KKG_OJ_BASE_URL=http://host.docker.internal:8121
```

## 后续实现顺序

1. 将 `rag.StaticRetriever` 替换为 pgvector Retriever。
2. 增加文档入库任务：loader、chunker、embedding、indexer、版本审计。
3. 接入真实 ChatModel，并将 prompt 模板移入可测试组件。
4. 将 KKG OJ、博客、文件上传、题解任务接口逐步包装为 Eino tools。
5. 为 graph 增加条件边、失败降级和工具权限策略。

更多 Eino 边界见 [Eino 业务流程约束](eino-workflow.md)。
