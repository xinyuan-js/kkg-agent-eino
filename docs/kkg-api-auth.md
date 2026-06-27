# KKG 鉴权与 API 契约

本文档来自对相邻 KKG 项目的源码阅读。KKG 已重构为单体后端：

- Backend: `/Users/zhuojianshuo/GolandProjects/awesomeProject/kkg-backend`

Blog 与 OJ 共享同一个 HTTP 服务，默认本地地址为 `http://127.0.0.1:8080`。

## 统一响应

两个后端都使用相近的 envelope：

```json
{
  "code": 0,
  "message": "ok",
  "data": {}
}
```

Blog HTTP 状态码和 `code` 基本一致：`400`、`401`、`403`、`500`。OJ 使用业务码：

- `0`: success
- `40000`: params error
- `40100`: not login
- `40101`: no auth
- `40300`: forbidden
- `40400`: not found
- `50000`: system error
- `50001`: operation error
- `50010`: api request error

## Blog 鉴权

Blog 鉴权实现位于：

- `internal/handler/auth_handler.go`
- `internal/middleware/jwt.go`
- `pkg/security/jwt.go`

登录后签发：

- `access_token`: 30 分钟，JWT `token_type=access`
- `refresh_token`: 7 天，JWT `token_type=refresh`

Token claims：

```json
{
  "user_id": 1,
  "role": "user",
  "token_type": "access",
  "iat": 0,
  "exp": 0
}
```

传递方式：

- 优先读取 `access_token` cookie。
- 也支持 `Authorization: Bearer <token>`。
- cookie 为 HttpOnly，SameSite=Lax，TLS 请求下 secure=true。

角色：

- `user`
- `admin`
- `super_admin`

状态：

- `status=1`: active
- `status=0`: disabled
- 其他状态：隐藏或不存在语义

## Blog 可工具化接口

公开接口：

- `POST /api/v1/auth/register`
- `POST /api/v1/auth/login`
- `POST /api/v1/auth/refresh`
- `POST /api/v1/auth/logout`
- `GET /api/v1/posts`
- `GET /api/v1/feed`
- `GET /api/v1/rankings/posts`
- `GET /api/v1/posts/:id`
- `GET /api/v1/posts/:id/engagement`
- `GET /api/v1/posts/:id/comments`
- `GET /api/v1/users/:id`
- `GET /api/v1/tweets/search`
- `GET /api/v1/search?type=post|user&q=...&limit=20`
- `GET /api/v1/search/suggest?type=post|user&q=...&limit=5`

登录后接口：

- `GET /api/v1/me/profile`
- `PUT /api/v1/me/profile`
- `PUT /api/v1/me/password`
- `GET /api/v1/me/notifications`
- `POST /api/v1/me/notifications/:id/read`
- `GET /api/v1/me/posts`
- `GET /api/v1/me/favorites`
- `POST /api/v1/posts`
- `PUT /api/v1/posts/:id/meta`
- `DELETE /api/v1/posts/:id`
- `GET /api/v1/posts/:id/drafts`
- `POST /api/v1/posts/:id/drafts`
- `GET /api/v1/posts/:id/drafts/:version`
- `PUT /api/v1/posts/:id/drafts/:version`
- `DELETE /api/v1/posts/:id/drafts/:version`
- `POST /api/v1/posts/:id/drafts/:version/publish`
- `GET /api/v1/posts/:id/versions`
- `POST /api/v1/posts/:id/publish`
- `POST /api/v1/posts/:id/unpublish`
- `POST /api/v1/posts/:id/rollback/:version`
- `POST /api/v1/posts/:id/comments`
- `POST /api/v1/posts/:id/like`
- `POST /api/v1/posts/:id/favorite`
- `POST /api/v1/uploads/image`
- `POST /api/v1/tweets`

管理接口：

- `GET /api/v1/admin/posts`
- `GET /api/v1/admin/users`
- `PUT /api/v1/admin/users/role`
- `DELETE /api/v1/admin/users/:id`
- `GET /api/v1/admin/audits`
- `POST /api/v1/admin/audits`

## OJ 鉴权

OJ 鉴权实现位于：

- `internal/middleware/auth.go`
- `internal/service/user_service.go`
- `internal/app/router.go`

OJ 支持两套登录态：

1. 自身 session：`sessions.Default(c)` 中的 `user_login`。
2. Blog 共享 JWT：读取 `access_token` cookie 或 Bearer token，使用同一个 JWT secret 解析 `user_id`。

共享登录流程：

1. 解析 Blog JWT。
2. 检查 `token_type` 为空或 `access`。
3. 用 `user_id` 查询共享 `users` 表。
4. 通过 `EnsureFromSharedUserID` 在 OJ `user` 表创建或同步用户。
5. 写入 Gin context：`loginUserId`。

这意味着 Agent API 只要透传 Blog `access_token`，就能调用 OJ 的登录后接口。

## OJ 可工具化接口

OJ 已统一挂载在 `/api/v1/oj` 前缀下；下面路径均省略此前缀。

用户：

- `POST /user/register`
- `POST /user/login`
- `GET /user/login/wx_open`
- `POST /user/logout`
- `GET /user/get/login`
- `POST /user/update/my`
- `GET /user/get/vo`
- `POST /user/list/page/vo`

题目：

- `POST /question/add`
- `POST /question/delete`
- `POST /question/update`
- `GET /question/get`
- `GET /question/get/vo`
- `POST /question/list/page/vo`
- `POST /question/my/list/page/vo`
- `POST /question/list/page`
- `POST /question/edit`
- `POST /question/run`
- `POST /question/question_submit/do`
- `POST /question/question_submit/list/page`
- `GET /question/rank/first-ac-24h`
- `GET /question/submission/events`

题解绑定与智能体任务：

- `POST /question/solution/bind`
- `POST /question/solution/unbind`
- `POST /question/solution/list/page`
- `POST /question/agent/solution/generate`
- `GET /question/agent/solution/task`
- `POST /question/agent/solution/task/list/page`

文件：

- `POST /file/upload`

## Agent 工具化优先级

第一阶段建议包装为只读或低风险工具：

- Blog search：`GET /api/v1/search`
- Blog post get：`GET /api/v1/posts/:id`
- OJ question get VO：`GET /question/get/vo`
- OJ solution list：`POST /question/solution/list/page`
- OJ first AC rank：`GET /question/rank/first-ac-24h`

第二阶段再开放写操作：

- 创建博客草稿
- 发布题解
- 绑定题目与题解
- 触发 OJ agent solution task

写操作必须增加：

- 用户角色检查
- 工具级 allowlist
- 请求审计
- 幂等键
- 人工确认或管理员确认
