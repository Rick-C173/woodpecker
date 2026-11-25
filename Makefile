# ==============================================================================
# Woodpecker(啄木鸟)— AI 代码/技术文档知识库 · 开发工作流
#
# 跨平台:Windows / Linux / macOS 通用,只依赖 Go 与 GNU Make(命令全部走 go/git 工具链)
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
.PHONY: help run api worker build test cover cover-html integration fmt vet lint lint-fix tidy web-install web web-test web-lint web-typecheck web-build api-docs api-sync api-sync-overwrite api-backup clean

## ---- Help ----
help:
	@echo Woodpecker dev commands:
	@echo   make run MODE=all,api,worker   start service (default all)
	@echo   make api                       start HTTP server only
	@echo   make worker                    start ingest worker only
	@echo   make build                     build to bin/ (platform suffix auto)
	@echo   make test                      full unit tests
	@echo   make cover                     unit tests + coverage summary
	@echo   make cover-html                generate coverage.html
	@echo   make integration               integration tests (TEST_DSN overridable)
	@echo   make lint                      golangci-lint run (see .golangci.yml)
	@echo   make lint-fix                  golangci-lint run --fix
	@echo   make fmt                       golangci-lint fmt
	@echo   make vet                       go vet ./...
	@echo   make tidy                      go mod tidy
	@echo   make api-docs                  swag init -> docs/swagger.json (OpenAPI source of truth)
	@echo   make api-sync                  sync spec to Apifox (ignore: add-only)
	@echo   make api-sync-overwrite        sync spec to Apifox (full overwrite)
	@echo   make api-backup                snapshot Apifox project to $(BACKUP_DIR)/
	@echo   make docker-build              build production images (server + web)
	@echo   make docker-up                 start production stack (see deploy/.env.production.example)
	@echo   make docker-down               stop production stack
	@echo   make web-install               install frontend deps (npm ci)
	@echo   make web                       frontend dev server (Vite, 5173, /api proxy 8080)
	@echo   make web-test                  frontend unit tests (vitest)
	@echo   make web-lint                  frontend ESLint
	@echo   make web-typecheck             frontend TypeScript check
	@echo   make web-build                 frontend production build (web/dist)
	@echo   make clean                     remove build/runtime artifacts (bin/, coverage, data, logs)

## ---- 运行 ----
run:
	go run $(SERVER_DIR) --mode=$(MODE)

api:
	go run $(SERVER_DIR) --mode=api

worker:
	go run $(SERVER_DIR) --mode=worker

## ---- 构建 ----
build:
	go build -o $(BIN) $(SERVER_DIR)
	@echo built $(BIN)

## ---- 测试 ----
test:
	go test -count=1 $(PKG)

cover:
	go test -count=1 $(PKG) -coverprofile=$(COVER_OUT)
	go tool cover -func=$(COVER_OUT)

cover-html:
	go test -count=1 $(PKG) -coverprofile=$(COVER_OUT)
	go tool cover -html=$(COVER_OUT) -o $(COVER_HTML)
	@echo generated $(COVER_HTML)

# 集成测试默认连独立测试库 woodpecker_test(与开发库隔离,可用 TEST_DSN 覆盖)；
# 测试库需预先以超级用户安装 vector 扩展:CREATE EXTENSION vector;
# -p 1:多个 IT 包(integration/repo/qa_probe)串行跑,避免并行 itTruncate 互相清表。
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
	docker build -t woodpecker-server:latest .
	docker build -t woodpecker-web:latest ./web
	@echo built images: woodpecker-server / woodpecker-web

docker-up:
	docker compose up -d --build
	@echo started; first deploy -> http://localhost:8080 (see docker-compose.yml header)

docker-down:
	docker compose down

## ---- 前端(web/,Node 22+)----

web-install:
	cd web && npm ci

# Vite dev server:/api 经代理转发 localhost:8080(SSE 直通),需后端先启动
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

## ---- 清理 ----
clean:
	go clean -testcache
	rm -rf bin data logs .apifox .trae-html-share-packages web/dist
	rm -f coverage.out coverage_integration.out coverage.html coverage \
		it_nocoverpkg jwt_coverpkg integration-test.log
	rm -f wp_cov* *.log
	@echo build/runtime artifacts cleaned

## ---- 接口文档(Apifox 同步,详见 md/Apifox同步.md)----

# 从 handler swaggo 注解生成 OpenAPI spec(docs/swagger.json 是接口契约的单一事实源)
api-docs:
	swag init -g cmd/server/main.go -o $(DOCS_DIR) --parseInternal
	@echo generated $(DOCS_DIR)/swagger.json

# 日常同步:只把新增接口导入 Apifox,已有接口(含云端精修的描述/示例)一律不动。
# 适合「新增接口」场景；修改已有接口后请用 api-sync-overwrite。
api-sync: api-docs
	python scripts/apifox_sync.py ignore

# 全量刷新:以 docs/swagger.json 为准覆盖 Apifox 同名接口(云端描述/示例会被 spec 取代)。
# 使用前提:接口的 swaggo 注解已维护完整(描述/参数/错误码),否则先补注解再跑。
api-sync-overwrite: api-docs
	python scripts/apifox_sync.py overwrite

# 导出 Apifox 项目完整快照(Apifox 格式,可整体导回恢复),修改前先备份。
api-backup:
	python scripts/apifox_sync.py backup
