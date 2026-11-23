# ==============================================================================
# Woodpecker（啄木鸟）— AI 代码/技术文档知识库 · 开发工作流
#
# 常用入口：make run / make test / make cover / make integration / make help
# 说明：
#   - Windows 下需 GNU Make（choco install make 或 scoop install make）
#   - run/build 目标会先打印 banner
#   - integration/clean 目标显式调用 PowerShell（避免 cmd/sh 差异）
# ==============================================================================

SERVER_DIR := ./cmd/server
BIN        := bin/woodpecker.exe
PKG        := ./...
COVER_OUT  := coverage.out
COVER_HTML := coverage.html
TEST_DSN   ?= postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker?sslmode=disable
MODE       ?= all

.DEFAULT_GOAL := help
.PHONY: help banner run api worker build test cover cover-html integration fmt vet lint tidy clean

## ---- 帮助 ----
help:
	@echo Woodpecker 开发命令：
	@echo   make run MODE=all,api,worker   启动服务，默认 all，先打印 banner
	@echo   make api                       仅启动 HTTP 服务
	@echo   make worker                    仅启动摄取 Worker
	@echo   make build                     编译到 bin/woodpecker.exe
	@echo   make test                      全量单元测试
	@echo   make cover                     单测 + 语句覆盖率汇总
	@echo   make cover-html                生成 coverage.html 覆盖率详情
	@echo   make integration               集成测试（连本机 PostgreSQL 5432）
	@echo   make fmt                       go fmt ./...
	@echo   make vet                       go vet ./...
	@echo   make lint                      golangci-lint run（配置见 .golangci.yml）
	@echo   make tidy                      go mod tidy
	@echo   make clean                     清理 bin 与覆盖率产物

## ---- Banner ----
# 用户自定义 banner（banner.txt）；经 PowerShell 输出以保持各 shell 下排版一致
banner:
	@powershell -NoProfile -Command "Write-Host ''; Get-Content -LiteralPath banner.txt -Encoding UTF8; Write-Host ''"
## ---- 运行 ----
run: banner
	go run $(SERVER_DIR) --mode=$(MODE)

api: banner
	go run $(SERVER_DIR) --mode=api

worker: banner
	go run $(SERVER_DIR) --mode=worker

## ---- 构建 ----
build: banner
	go build -o $(BIN) $(SERVER_DIR)
	@echo 已生成 $(BIN)

## ---- 测试 ----
test:
	go test -count=1 $(PKG)

cover:
	go test -count=1 $(PKG) -coverprofile=$(COVER_OUT)
	go tool cover -func=$(COVER_OUT)

cover-html:
	go test -count=1 $(PKG) -coverprofile=$(COVER_OUT)
	go tool cover -html=$(COVER_OUT) -o $(COVER_HTML)
	@echo 已生成 $(COVER_HTML)

integration:
	powershell -NoProfile -Command "$$env:TEST_DSN='$(TEST_DSN)'; go test -count=1 -tags=integration ./internal/integration"

## ---- 代码质量 ----
fmt:
	go fmt $(PKG)

vet:
	go vet $(PKG)

lint:
	golangci-lint run

tidy:
	go mod tidy

audit:
	go build ./...
	go vet ./...
	golangci-lint run ./...
	go test ./... -count=1 -race

## ---- 清理 ----
clean:
	powershell -NoProfile -Command "Remove-Item -Recurse -Force bin -ErrorAction SilentlyContinue; Remove-Item -Force coverage.out, coverage_integration.out, coverage.html -ErrorAction SilentlyContinue"
	@echo 已清理构建与覆盖率产物
