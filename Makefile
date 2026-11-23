# ==============================================================================
# Woodpecker（啄木鸟）— AI 代码/技术文档知识库 · 开发工作流
#
# 跨平台：Windows / Linux / macOS 通用，只依赖 Go 与 GNU Make（命令全部走 go/git 工具链）
# ==============================================================================

SERVER_DIR := ./cmd/server
BIN        := bin/woodpecker$(shell go env GOEXE)
PKG        := ./...
COVER_OUT  := coverage.out
COVER_HTML := coverage.html
MODE       ?= all

.DEFAULT_GOAL := help
.PHONY: help run api worker build test cover cover-html integration fmt vet lint lint-fix tidy clean

## ---- 帮助 ----
help:
	@echo Woodpecker 开发命令：
	@echo   make run MODE=all,api,worker   启动服务，默认 all
	@echo   make api                       仅启动 HTTP 服务
	@echo   make worker                    仅启动摄取 Worker
	@echo   make build                     编译到 bin/（自动带平台后缀）
	@echo   make test                      全量单元测试
	@echo   make cover                     单测 + 语句覆盖率汇总
	@echo   make cover-html                生成 coverage.html 覆盖率详情
	@echo   make integration               集成测试（默认连 127.0.0.1:5432，可用 TEST_DSN 环境变量覆盖）
	@echo   make lint                      golangci-lint run（配置见 .golangci.yml）
	@echo   make lint-fix                  golangci-lint run --fix（含 gofmt/goimports 自动修复）
	@echo   make fmt                       golangci-lint fmt（格式化）
	@echo   make vet                       go vet ./...
	@echo   make tidy                      go mod tidy
	@echo   make clean                     清理 bin 与覆盖率产物

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
	go test -count=1 -tags=integration ./internal/integration

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

## ---- 清理 ----
clean:
	go clean -testcache
	git clean -fX -- bin coverage.out coverage_integration.out coverage.html
	@echo 已清理构建与覆盖率产物
