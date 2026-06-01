# KKG Agent Eino

KKG Agent Eino 是面向 KKG 项目的智能体工作台。前端使用 Nuxt + Vue，后端使用 Go + CloudWeGo Eino，把 chain、graph、RAG 向量库和 KKG 现有 API 工具化放在同一个工程边界内，但开发环境与 KKG 主项目分离。

## 当前骨架

- `apps/api`: Go Agent API，已接入 Eino `chain` 与 `graph` 两种编排入口。
- `apps/web`: Nuxt 前端，提供 chain/graph 模式切换、prompt 输入和运行结果查看。
- `infra/postgres`: pgvector 初始化脚本，作为 RAG 向量库的本地工业化基线。
- `.devcontainer`: 独立开发容器，包含 Go、Node、Postgres/pgvector、Redis、MinIO。
- `docs`: KKG 鉴权/API、架构和 RAG 流程说明。

## 本地启动

```bash
cp .env.example .env
docker compose -f docker-compose.dev.yml up -d postgres redis minio

cd apps/api
go mod download
go run ./cmd/agent-api

cd ../web
npm install
npm run dev
```

访问：

- Nuxt Web: `http://localhost:3000`
- Agent API: `http://localhost:8088/health`
- MinIO Console: `http://localhost:9001`

## API 快速验证

```bash
curl -sS http://localhost:8088/health
curl -sS -X POST http://localhost:8088/api/v1/agent/run \
  -H 'Content-Type: application/json' \
  -d '{"mode":"graph","query":"为 KKG OJ 题目生成题解思路"}'
```

## 与 KKG 项目的边界

本仓库不嵌入 KKG 后端源码。开发时通过 `.env` 中的 `KKG_BLOG_BASE_URL`、`KKG_OJ_BASE_URL` 指向独立运行的 KKG 服务；智能体后端把这些 API 包装成工具，沿用 KKG 的 `access_token` cookie 或 Bearer token。

更多细节见：

- [架构说明](docs/architecture.md)
- [Eino 业务流程约束](docs/eino-workflow.md)
- [KKG 鉴权与 API 契约](docs/kkg-api-auth.md)
- [RAG 向量库流程](docs/rag-pipeline.md)
