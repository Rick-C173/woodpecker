# Woodpecker API 设计

> REST + SSE。上游文档：[PRD.md](./PRD.md) · [数据库设计.md](./数据库设计.md)
> 后端 Gin，路由前缀 `/api/v1`。

## 1. 全局约定

| 项 | 约定 |
|---|---|
| 认证 | `Authorization: Bearer <JWT>`；claims 含 `sub`(user_id)、`adm`(is_admin)、`ver`(token_version)。`ver` 与 `users.token_version` 不一致 → 401（禁用/改密/**设撤管理员**立即失效；权限变更即失效旧 token） |
| 内容类型 | 请求/响应 `application/json`（除上传 multipart 与 SSE） |
| 命名 | 字段 snake_case；时间 RFC3339 UTC；ID 用 JSON number（bigint，内部规模无精度风险） |
| 成功响应 | 直接返回数据对象或 `{items, total, page, page_size}`（分页） |
| 错误响应 | `{"code": "KB_001", "message": "知识库名称已存在", "request_id": "..."}`，HTTP 状态码见 §2 |
| 分页 | `?page=1&page_size=20`（默认 20，最大 100） |
| 限流 | 全局 100 req/min/用户；问答接口 10 req/min/用户 → 429（M2 为进程内固定窗口实现，多实例部署时替换 Redis） |

**权限 → HTTP 状态码映射**（落实 PRD R6 不可见原则）：

| 场景 | 状态码 |
|---|---|
| 未登录 / token 失效 | 401 |
| 非成员访问知识库内任何资源 | **404**（按"不存在"返回，不泄露库名） |
| 执行超出自身角色的操作（如成员调管理者接口） | 403 `FORBIDDEN` |
| 普通用户访问管理端点 | 403 |
| 管理员执行被保护规则拦截（如禁用自己） | 422，业务码见 §6 |

## 2. 业务错误码

| code | HTTP | 含义 |
|---|---|---|
| AUTH_001 | 401 | 用户名或密码错误 |
| AUTH_002 | 401 | 账号已锁定（5 次失败锁 10 分钟） |
| AUTH_003 | 401 | 账号被禁用 |
| AUTH_004 | 401 | token 失效（版本号不符） |
| AUTH_005 | 403 | 自助注册未开放 |
| AUTH_006 | 422 | 密码不符合规则（≥ 8 位） |
| AUTH_007 | 403 | 需先修改初始密码（`must_change_password=true` 时除改密端点外一律 403） |
| NOT_FOUND | 404 | 资源不存在（如目标用户/文档已删除） |
| VALIDATION_FAILED | 422 | 参数校验失败 |
| USER_001 | 409 | 用户名已存在 |
| USER_002 | 422 | 不能禁用自己 |
| USER_003 | 422 | 最后一个管理员保护 |
| KB_001 | 409 | 知识库名称重复 |
| DOC_001 | 422 | 仓库超过大小/文件数限制 |
| DOC_002 | 422 | 文件格式不支持 |
| DOC_003 | 422 | 仓库凭证无效或地址不可达 |
| DOC_004 | 422 | 同步失败（远端不可达），旧内容保持可用 |
| MODEL_001 | 422 | 该用途缺少默认条目 |
| MODEL_002 | 422 | 默认条目不可删除（须先切换默认） |
| MODEL_003 | 409 | 模型服务下存在条目，不可删除 |
| MODEL_004 | 422 | 服务下存在默认条目，不可停用（须先把默认切到其他条目） |
| LLM_001 | 502 | 模型服务不可用 |
| LLM_002 | 502 | 模型服务无有效响应（测试连接失败详情） |
| SYS_001 | 503 | 数据库不可用（/healthz 探针） |
| DOC_TASK_RUNNING | 409 | 该文档已有运行中的摄取/同步任务 |
| RATE_LIMITED | 429 | 请求过于频繁 |

## 3. 端点目录

权限列含义：公开 / 登录 / 管理员 / 管理者 / 编辑者 / 成员（均含管理员越权；定义见 PRD R6：管理者可删改内容，编辑者可补充内容，成员只读）。

### 3.1 认证

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| POST | /auth/register | 公开（设置开启时） | 自助注册（默认关闭，FR-01） |
| POST | /auth/login | 公开 | 登录，返回 token 与 `must_change_password` |
| POST | /auth/password | 登录 | 修改自己密码（含首登强制改密），成功后 token_version+1 需重新登录。**首登未改密（`must_change_password=true`）时，除本端点与 `/healthz` 外所有接口返回 403 `AUTH_007`** |

### 3.2 用户管理（管理员）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | /admin/users | 管理员 | 用户列表（`?q=&status=&page=`） |
| POST | /admin/users | 管理员 | 创建用户，响应含一次性初始密码 |
| PATCH | /admin/users/{id} | 管理员 | 禁用/启用、设/撤管理员（触发 USER_002/003 校验） |
| POST | /admin/users/{id}/reset-password | 管理员 | 重置密码，响应含一次性初始密码 |

### 3.3 模型服务配置（管理员，FR-12）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | /admin/model-services | 管理员 | 服务列表（API Key 不回显，仅返回 `has_key: true`） |
| POST | /admin/model-services | 管理员 | 创建服务 |
| PATCH | /admin/model-services/{id} | 管理员 | 更新/启停（key 字段缺省=不修改；`enabled:false` 且服务持有默认条目 → 422 MODEL_004） |
| DELETE | /admin/model-services/{id} | 管理员 | 删除（存在条目时 409） |
| POST | /admin/model-services/{id}/test | 管理员 | 测试连接，响应 `{chat: ok/err, embedding: ok/err, rerank: ok/err}` |
| GET | /admin/model-entries | 管理员 | 条目列表（`?service_id=&purpose=`） |
| POST | /admin/model-entries | 管理员 | 创建条目 |
| PATCH | /admin/model-entries/{id} | 管理员 | 更新 / `is_default: true` 设默认（每用途唯一默认） |
| DELETE | /admin/model-entries/{id} | 管理员 | 删除（默认条目 → MODEL_002） |

### 3.4 系统设置与审计（管理员）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | /admin/settings | 管理员 | 排除规则、自助注册开关等 |
| PATCH | /admin/settings | 管理员 | 更新设置（记审计） |
| GET | /admin/audit-logs | 管理员 | `?user_id=&action=&object_type=&from=&to=&page=` |
| GET | /admin/feedbacks | 管理员 | 反馈列表（FR-14）：`?kb_id=&reason=&from=&to=&page=` |

### 3.5 知识库（FR-02）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | /kbs | 登录 | 我可见的库（管理员=全部），含 `my_role` |
| POST | /kbs | 管理员 | 建库 |
| GET | /kbs/{kb} | 成员 | 详情 |
| PATCH | /kbs/{kb} | 管理员 | 改名/描述 |
| DELETE | /kbs/{kb} | 管理员 | 删库（二次确认由前端承担） |
| GET | /kbs/{kb}/members | 管理员 | 成员列表 |
| POST | /kbs/{kb}/members | 管理员 | `{user_id, role: owner\|editor\|member}` |
| PATCH | /kbs/{kb}/members/{user} | 管理员 | 角色变更 |
| DELETE | /kbs/{kb}/members/{user} | 管理员 | 移除成员（其历史会话保留但失访） |

### 3.6 文档与摄取（FR-03/04/05/11）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | /kbs/{kb}/documents | 成员 | 文档列表（`?type=&status=&page=`）；操作类字段按权限返回 |
| POST | /kbs/{kb}/documents/git | 编辑者 | 接入仓库 `{repo_url, git_ref?, credential?, ignore_rules?}`；`credential` 为 `{"kind":"token|ssh","secret":"..."}`（`token`→HTTPS BasicAuth 密码位，用户名取 `GIT_TOKEN_USERNAME`，默认 `oauth2`；`ssh`→PEM 私钥走 PublicKeys + known_hosts 校验）。**兼容**：传裸字符串按 `kind=token` 处理 |
| POST | /kbs/{kb}/documents/upload | 编辑者 | multipart `files[]`，≤50 个/单文件 ≤5MB，不支持格式逐个报 DOC_002 |
| POST | /kbs/{kb}/documents/url | 编辑者 | `{url}` |
| GET | /kbs/{kb}/documents/{doc} | 成员 | 详情：状态、错误、文件清单（含"已排除"标记）、任务历史 |
| DELETE | /kbs/{kb}/documents/{doc} | 管理者 | 删除文档 |
| POST | /kbs/{kb}/documents/{doc}/retry | 编辑者 | 重试（仅失败态，产生新任务） |
| POST | /kbs/{kb}/documents/{doc}/reindex | 管理者 | 重建索引（仅已就绪态） |
| POST | /kbs/{kb}/documents/{doc}/sync | 编辑者 | 手动同步（仅 git 类型） |

### 3.7 检索与会话（FR-07~10, 14）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | /kbs/{kb}/search | 成员 | `?q=&page=` 纯检索，返回分块片段（含快照字段） |
| GET | /kbs/{kb}/conversations | 成员 | 我的会话列表 |
| POST | /kbs/{kb}/conversations | 成员 | 新建会话 |
| GET | /conversations/{c} | 所属用户/管理员 | 消息历史（含引用） |
| PATCH | /conversations/{c} | 所属用户 | 重命名 `{title}` |
| DELETE | /conversations/{c} | 所属用户 | 删除会话（反馈级联删） |
| POST | /conversations/{c}/messages | 所属用户 | **提问，SSE 响应**（见 §4） |
| POST | /messages/{m}/regenerate | 所属用户 | 重新生成，SSE；替换该回答内容 |
| POST | /messages/{m}/feedback | 所属用户 | 点踩 `{reason: 1-4, comment?}`（重复覆盖） |
| DELETE | /messages/{m}/feedback | 所属用户 | 撤销点踩 |

会话访问规则：`/conversations/{c}` 与 `/messages/*` 不在 `/kbs/{kb}` 分组下，中间件须先解析会话所属知识库并**校验成员资格（R6：非成员 → 404）**——被移除的成员立即失去历史会话访问；再校验会话所属用户（他人 → 404），管理员可访问任意会话。

## 4. SSE 问答协议

`POST /conversations/{c}/messages`，请求体 `{"content": "..."}`，响应 `Content-Type: text/event-stream`：

```text
event: delta       ← 回答增量（多次）
data: {"content": "该接口在 "}

event: delta
data: {"content": "internal/handler 中实现"}

event: citations   ← 引用列表（生成完成后、done 之前一次）
data: {"items": [
  {"seq": 1, "document_id": 12, "file_path": "internal/handler/auth.go",
   "line_start": 45, "line_end": 62, "heading_path": null,
   "source_url": null, "snippet": "func Login(c *gin.Context)..."}
]}

event: done        ← 结束
data: {"message_id": 8821, "prompt_tokens": 1820, "completion_tokens": 431}

event: error       ← 异常终止
data: {"code": "LLM_001", "message": "模型服务不可用"}
```

规则：

- **引用只包含回答里真正标注过的资料**：服务端解析回答正文中的 `[n]`，按首次出现顺序去重，
  只发被引用且在资料范围内的条目，`seq` 按「回答中首次出现顺序」从 1 重新编号；
  检索命中但回答未引用的一律不发、不落库（审计 P1-16）。
- 依据不足时：正常输出说明文字，**不发 citations 事件**，直接 `done`（PRD FR-07）；
  检索有命中但回答一个 `[n]` 都没用时同样不发 citations 帧。
- 停止生成 = 客户端断开连接；服务端监听 `r.Context().Done()`，已生成的部分照常落库（role=assistant，无 citations）。
- 心跳：每 15s 发 `event: ping`，防止代理断连。
- 流式期间消息不落库，`done` 时才写库（含 tokens 与 citations）；断开也落库，标记 `interrupted`（detail 字段）。

## 5. 关键请求/响应示例

**登录**

```json
// POST /auth/login
{"username": "rick", "password": "***"}
// 200
{"token": "eyJ...", "must_change_password": true, "is_admin": false}
// 401
{"code": "AUTH_002", "message": "账号已锁定，请 10 分钟后重试", "request_id": "req_01H..."}
```

**接入 Git 仓库**

```json
// POST /kbs/1/documents/git
{
  "repo_url": "https://git.internal.com/team/order-service.git",
  "credential": {"kind": "token", "secret": "ghp_xxx"},
  "ignore_rules": ["docs/drafts/**"]
}
// 202
{"id": 12, "type": 1, "name": "order-service", "status": 1}

// SSH 私有仓库：credential 换成私钥（PEM）；repo_url 用 git@ / ssh:// 形式
{"repo_url": "git@git.internal.com:team/order-service.git",
 "credential": {"kind": "ssh", "secret": "-----BEGIN OPENSSH PRIVATE KEY-----..."}}
```

**搜索**

```json
// GET /kbs/1/search?q=限流实现&page=1
{
  "items": [
    {"chunk_id": 3091, "document_id": 12, "file_path": "internal/middleware/ratelimit.go",
     "line_start": 20, "line_end": 48, "content": "...", "score": 0.0317}
  ],
  "total": 7, "page": 1, "page_size": 20
}
```

## 6. 实现备注

- 路由分组即权限中间件挂载点：`/admin/*` 挂管理员中间件；`/kbs/{kb}/*` 挂"库成员资格"中间件（解析 kb → 非成员 404），其内按 R6 分级挂中间件：接入/上传/重试/同步挂"编辑者"，删除/重建挂"管理者"。
- 所有写操作（含越权尝试）写审计（数据库设计 §5 动作枚举）。
- 上传接口先整体校验数量/大小/格式再落盘，失败不留残留文件（PRD FR-04）。
- 重试/重建/同步接口均为 202 + 任务幂等：同文档存在运行中任务时返回 409（`{"code": "DOC_TASK_RUNNING"}`）。
- OpenAPI 规范：开发期用 swaggo 从注解生成 `/swagger`，作为前后端联调契约。
