# ==============================================================================
# Woodpecker(啄木鸟)— AI 代码/技术文档知识库 · 开发工作流
#
# 跨平台：Windows / Linux / macOS 通用,只依赖 Go 与 GNU Make(命令全部走 go/git 工具链)
# ==============================================================================

SERVER_DIR := ./cmd/server
BIN        := bin/woodpecker$(shell go env GOEXE)
PKG        := ./...
COVER_OUT  := coverage.out
COVER_HTML := coverage.html
MODE       ?= all

# Apifox 接口文档同步(项目 ID 与冲突策略见 md/Apifox同步.md)
APIFOX_PROJECT ?= 9076931
DOCS_DIR       := docs
BACKUP_DIR     := bin/apifox-backup

.DEFAULT_GOAL := help
.PHONY: help run api worker build build-all test cover cover-html integration fmt vet lint lint-fix tidy web-install web web-test web-e2e web-lint web-typecheck web-build api-docs api-sync api-sync-overwrite api-backup clean

## ---- 帮助 ----
help:
	@-chcp 65001 >nul
	@echo Woodpecker 开发命令:
	@echo   make run MODE=all,api,worker   启动服务,默认 all
	@echo   make api                       仅启动 HTTP 服务
	@echo   make worker                    仅启动摄取 Worker
	@echo   make build                     编译到 bin/(自动带平台后缀)
	@echo   make build-all                 单二进制构建(内嵌前端产物,部署/发布用)
	@echo   make test                      全量单元测试
	@echo   make cover                     单测 + 语句覆盖率汇总
	@echo   make cover-html                生成 coverage.html 覆盖率详情
	@echo   make integration               集成测试(默认连 127.0.0.1:5432,可用 TEST_DSN 环境变量覆盖)
	@echo   make lint                      golangci-lint run(配置见 .golangci.yml)
	@echo   make lint-fix                  golangci-lint run --fix(含 gofmt/goimports 自动修复)
	@echo   make fmt                       golangci-lint fmt(格式化)
	@echo   make vet                       go vet ./...
	@echo   make tidy                      go mod tidy
	@echo   make api-docs                  swag init 生成 docs/swagger.json(OpenAPI 事实源)
	@echo   make api-sync                  生成 spec 并同步 Apifox(ignore：只新增,不动已有接口)
	@echo   make api-sync-overwrite        生成 spec 并全量覆盖 Apifox(接口定义变更后用)
	@echo   make api-backup                导出 Apifox 项目完整快照到 $(BACKUP_DIR)/
	@echo   make docker-build              构建生产镜像(server + web)
	@echo   make docker-up                 生产编排启动(需先配置 .env,见 deploy/.env.production.example)
	@echo   make docker-down               停止生产编排
	@echo   make web-install               前端安装依赖(npm ci)
	@echo   make web                       前端开发服务器(Vite,5173,/api 代理 8080)
	@echo   make web-test                  前端单元测试(vitest)
	@echo   make web-lint                  前端 ESLint
	@echo   make web-typecheck             前端 TypeScript 类型检查
	@echo   make web-build                 前端生产构建(web/dist)
	@echo   make web-e2e                   前端 e2e 五步冒烟(需后端 8080 与 E2E_PASSWORD)
	@echo   make clean                     清理所有编译与运行生成的文件(bin/覆盖率/data/日志等)
	@-chcp 936 >nul

## ---- 运行 ----
run:
	go run $(SERVER_DIR) --mode=$(MODE)

api:
	go run $(SERVER_DIR) --mode=api

worker:
	go run $(SERVER_DIR) --mode=worker

## ---- 构建 ----
build:
	@-chcp 65001 >nul
	go build -o $(BIN) $(SERVER_DIR)
	@echo 已生成 $(BIN)
	@-chcp 936 >nul

# 单二进制构建（含内嵌前端）：前端构建 → 拷贝 dist → 后端编译。
# 裸机部署（scripts/deploy）与 Docker 镜像均走此链路；仅后端开发用 build 即可。
build-all: web-build
	go run scripts/embed_web.go
	@$(MAKE) build

## ---- 测试 ----
test:
	go test -count=1 $(PKG)

cover:
	go test -count=1 $(PKG) -coverprofile=$(COVER_OUT)
	go tool cover -func=$(COVER_OUT)

cover-html:
	@-chcp 65001 >nul
	go test -count=1 $(PKG) -coverprofile=$(COVER_OUT)
	go tool cover -html=$(COVER_OUT) -o $(COVER_HTML)
	@echo 已生成 $(COVER_HTML)
	@-chcp 936 >nul

# 集成测试默认连独立测试库 woodpecker_test(与开发库隔离,可用 TEST_DSN 覆盖)；
# 测试库需预先以超级用户安装 vector 扩展：CREATE EXTENSION vector;
# -p 1：多个 IT 包(integration/repo/qa_probe)串行跑,避免并行 itTruncate 互相清表。
integration:
	go test -count=1 -p 1 -tags=integration ./internal/integration ./internal/repo ./internal/qa_probe

## ---- 代码质量 ----
fmt:
	golangci-lint fmt

vet:
	go vet $(PKG)

lint:
	golangci-lint run

lint-fix:
	golangci-lint run --fix

tidy:
	go mod tidy

## docker部署
docker-build:
	@-chcp 65001 >nul
	docker build -t woodpecker-server:latest .
	docker build -t woodpecker-web:latest ./web
	@echo 已构建 woodpecker-server / woodpecker-web 镜像
	@-chcp 936 >nul

docker-up:
	@-chcp 65001 >nul
	docker compose up -d --build
	@echo 已启动；首次部署打开 http://localhost:8080? 见 docker-compose.yml 头部说明
	@-chcp 936 >nul

docker-down:
	docker compose down

## ---- 前端(web/,Node 22+)----

web-install:
	cd web && npm ci

# Vite dev server：/api 经代理转发 localhost:8080(SSE 直通),需后端先启动
web:
	cd web && npm run dev

web-test:
	cd web && npm run test

web-lint:
	cd web && npm run lint

web-typecheck:
	cd web && npm run typecheck

web-build:
	cd web && npm run build

# e2e 五步冒烟(登录→建库→上传→搜索→聊天)：需后端 :8080 先启动；
# 凭证经环境变量注入(不进仓库)：E2E_USERNAME(默认 root) / E2E_PASSWORD
web-e2e:
	cd web && npm run test:e2e

## ---- 清理 ----
clean:
	go clean -testcache
	python scripts/clean_artifacts.py

## ---- 接口文档(Apifox 同步,详见 md/Apifox同步.md)----

# 从 handler swaggo 注解生成 OpenAPI spec(docs/swagger.json 是接口契约的单一事实源)
api-docs:
	@-chcp 65001 >nul
	swag init -g cmd/server/main.go -o $(DOCS_DIR) --parseInternal
	@echo 已生成 $(DOCS_DIR)/swagger.json
	@-chcp 936 >nul

# 日常同步：只把新增接口导入 Apifox,已有接口(含云端精修的描述/示例)一律不动。
# 适合「新增接口」场景；修改已有接口后请用 api-sync-overwrite。
api-sync: api-docs
	python scripts/apifox_sync.py ignore

# 全量刷新：以 docs/swagger.json 为准覆盖 Apifox 同名接口(云端描述/示例会被 spec 取代)。
# 使用前提：接口的 swaggo 注解已维护完整(描述/参数/错误码),否则先补注解再跑。
api-sync-overwrite: api-docs
	python scripts/apifox_sync.py overwrite

# 导出 Apifox 项目完整快照(Apifox 格式,可整体导回恢复),修改前先备份。
api-backup:
	python scripts/apifox_sync.py backup
