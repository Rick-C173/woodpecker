# Woodpecker（啄木鸟）

AI 驱动的代码/技术文档知识库：把仓库、文档与网页摄取为可检索的知识资产，通过混合检索与流式问答获得带引用来源的回答。

## 功能特性

- **知识库管理**：多知识库、成员角色（owner/editor/member）、管理员越权审计
- **多源摄取**：文件上传、Git 仓库（HTTPS/SSH，支持凭证）、网页 URL；分块 → 向量化 → 入库，任务状态机（pending/running/ready/failed）+ fencing 防僵尸双写 + 递增重试
- **混合检索**：pgvector HNSW 向量检索 + trigram 关键词兜底，RRF 融合（k=60）+ 可选 rerank
- **流式问答**：SSE（delta → citations → done），回答带 `[n]` 引用标注，引用明细与落库一致；多轮上下文；重新生成
- **反馈闭环**：点踩原因（1-4）+ 说明，管理端可查
- **用户管理**：自助注册（首管理员引导）、强制改密、锁定/禁用、审计日志
- **模型配置**：管理端配置 OpenAI 兼容服务（embeddings/chat/rerank），支持本机 Ollama

## 技术栈

Go（Gin + GORM + asynq）· PostgreSQL 17 + pgvector（vector(1024)）· Redis · golang-migrate · golangci-lint

## 快速开始

依赖：Go 1.27+、PostgreSQL（需 `pgvector` 扩展）、Redis。备用 Docker 栈见 `docker-compose.dev.yml`（映射 5433/6380）。

```bash
# 1. 准备配置
cp .env.example .env    # 按需修改；开发默认连本机 5432/6379

# 2. 启动（API + Worker 同进程；数据库迁移自动执行）
make run                # 等价于 go run ./cmd/server --mode=all
# 仅 API：make api      # 仅 Worker：make worker

# 3. 健康检查
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz   # {"db":"up","redis":"up"}
```

> 生产环境必须显式注入 `JWT_SECRET` 与 `ENCRYPTION_KEY`（AES-256-GCM，32 字节）并将 `APP_ENV=production`，否则拒绝启动。

### 首次登录（无默认账号）

系统**没有内置账号**：`POST /api/v1/auth/register` 注册的**第一个用户自动成为管理员**（首个用户只能由 API 直接注册产生）。本机开发环境当前管理员为：

| 用户名 | 密码 | 说明 |
|--------|------|------|
| `root` | `password8` | 开发库 bootstrap 注册的管理员（仅本机测试环境） |

管理员创建的用户（`POST /api/v1/admin/users`）首次登录强制改密（AUTH_007）。

### 模型服务配置

登录管理员后在管理端配置（OpenAI 兼容 API，`api_key` 任意值也可）：

```bash
# 例：接本机 Ollama（需先 ollama pull bge-m3 qwen2.5:7b）
curl -X POST http://127.0.0.1:8080/api/v1/admin/model-services -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"name":"ollama-local","base_url":"http://127.0.0.1:11434/v1","api_key":"ollama"}'
curl -X POST http://127.0.0.1:8080/api/v1/admin/model-entries -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"service_id":1,"purpose":"embedding","model_name":"bge-m3","is_default":true}'
curl -X POST http://127.0.0.1:8080/api/v1/admin/model-entries -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"service_id":1,"purpose":"chat","model_name":"qwen2.5:7b","is_default":true}'
```

> 向量维度需与 `chunks.embedding` 的 `vector(1024)` 一致，或在模型条目 `params.dimensions` 显式声明。

## 开发命令

```bash
make help          # 全部命令
make build         # 编译到 bin/
make test          # 全量单元测试
make integration   # 集成测试（连 127.0.0.1:5432，可用 TEST_DSN 覆盖）
make lint          # golangci-lint run
make vet / fmt     # 静态检查 / 格式化
make clean         # 清理编译与运行产物（白名单制，不动 docs/ 与 md/）
make api-docs      # swag init 生成 docs/swagger.json（OpenAPI 事实源）
```

## API 文档

- OpenAPI 事实源：`docs/swagger.json`（由 handler 注解 `make api-docs` 生成）
- 接口契约同步 Apifox：`make api-sync`（只新增）/ `make api-sync-overwrite`（全量覆盖），同步前自动备份
- 统一响应：成功直接返回数据对象/分页信封；失败 `{"code","message","request_id"}`，错误码见 `internal/httpx/errcode.go`

## MCP Server（AI 辅助开发接入）

IDE Agent（Cursor / Claude Code / Trae）可通过 MCP 协议查询知识库（检索 + 带引用问答）：

```bash
go build -o woodpecker-mcp.exe ./cmd/mcp-server
woodpecker-mcp -server http://localhost:8080 -token wpk_xxx   # token 在网页端个人 API Token 页生成
```

四工具：`list_kbs` / `search` / `ask` / `get_document`；鉴权与权限沿用个人 API Token（M15）。接入配置与排障见 [md/MCP接入指南.md](./md/MCP接入指南.md)。

## 目录结构

```
cmd/server/            服务入口（--mode=all|api|worker）
cmd/mcp-server/        MCP Server 入口（stdio，独立进程）
internal/config        环境配置（.env + .env.{APP_ENV} 分环境加载）
internal/handler       HTTP 层（swaggo 注解）
internal/service       业务层
internal/repo          存储层（检索 SQL 手写，见 md/数据库设计.md）
internal/worker        摄取流水线（asynq worker）
internal/llm           Provider（OpenAI 兼容 embeddings/chat/rerank）
internal/rag           分块
internal/mcpserver     MCP 工具层（四工具 + SSE 消费）
migrations/            嵌入 SQL 迁移（自研 runner，schema.up.sql 基线 + 开发期 patch）
docs/                  swag 生成的 OpenAPI spec
```

## 已知限制

- 集成测试与探针测试默认连独立测试库 `woodpecker_test`（建库后需以超级用户安装 `vector` 扩展，`pg_trgm` 可由普通用户自装），不再影响开发库数据；CI 或其他环境用 `TEST_DSN` 覆盖
- 模型连通性测试接口对 embedding+chat 双模型串行冷启动可能超过客户端超时
- 引用事件在生成完成落库后才发送；客户端中途断开时正文仍带 `[n]` 标记可自行解析
