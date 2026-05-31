# RAG 向量库流程

## 基线选择

当前开发容器使用 PostgreSQL 16 + pgvector。原因：

- 本地启动成本低，和业务元数据可以在同一个数据库事务中维护。
- 适合早期验证 chunk、embedding、rerank、权限过滤和审计。
- 后续可在 `internal/rag.Retriever` 接口后替换为 Milvus、Qdrant、Elasticsearch dense vector 或云向量库。

## 数据表

初始化脚本位于 `infra/postgres/001_init.sql`。

核心表：`rag_documents`

- `source`: 来源，如 `blog_post`、`oj_question`、`oj_solution`、`manual_doc`
- `external_id`: KKG 侧原始 ID 或稳定业务键
- `title`: 标题
- `content`: chunk 内容
- `metadata`: JSONB 元数据
- `embedding`: `vector(1536)`
- `content_hash`: 内容哈希，用于增量同步

## 入库流水线

标准流程：

1. Loader：从 KKG API、数据库只读副本、Markdown 或对象存储读取原文。
2. Normalizer：清洗 HTML、Markdown、代码块和无效字符。
3. Chunker：按语义段落、代码块、题目字段分块。
4. Metadata Enricher：写入来源、作者、权限、标签、更新时间、业务 ID。
5. Embedding：调用 embedding 模型生成向量。
6. Indexer：upsert 到 pgvector，记录 `content_hash`。
7. Verifier：抽样查询召回质量，输出离线评估结果。

## 查询流水线

Agent graph 中建议拆成独立节点：

1. Query Rewrite：结合用户意图、题目 ID、历史上下文生成检索 query。
2. Hybrid Search：向量召回 + 关键词召回。
3. Permission Filter：按用户角色、可见范围、资源状态过滤。
4. Rerank：对 topN 候选重排。
5. Context Builder：压缩引用片段，保留来源和证据链。
6. Synthesis：交给模型生成答案。

## 来源规划

第一批来源：

- `blog_post`: 已发布博客文章
- `oj_question`: OJ 题面、标签、样例、判题配置
- `oj_solution`: 已绑定或生成的题解
- `manual_doc`: 项目手写文档、运维手册、规范

## 权限策略

RAG 检索不能只做向量相似度，需要把 KKG 权限放入 metadata：

```json
{
  "visibility": "public",
  "owner_id": "123",
  "roles": ["admin"],
  "status": "published"
}
```

查询时规则：

- 未登录用户只能召回 public + published。
- 普通用户可召回自己的 private 草稿。
- admin 可召回管理范围内内容。
- super_admin 可召回全部内容，但仍应写审计。

## 与 Eino 的边界

`internal/rag.Retriever` 是当前稳定接口。Eino 节点只依赖：

```go
type Retriever interface {
    Retrieve(ctx context.Context, query Query) ([]Document, error)
}
```

因此后续实现 pgvector、混合搜索、rerank 时，不需要改动 `internal/agent` 的编排主流程。
