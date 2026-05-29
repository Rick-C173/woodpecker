.PHONY: run build test clean deps fmt lint verify

# 变量
BINARY_NAME=woodpecker
BUILD_DIR=build
GO=go
GOFLAGS=-v

# 默认目标
all: deps fmt build

# 安装依赖
deps:
	$(GO) mod tidy
	$(GO) mod download

# 格式化代码
fmt:
	$(GO) fmt ./...

# 代码检查
lint:
	@which golangci-lint > /dev/null || (echo "请安装 golangci-lint: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)
	golangci-lint run ./...

# 编译
build:
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) .

# 编译（优化）
build-prod:
	$(GO) build -ldflags="-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) .

# 运行
run:
	$(GO) run main.go

# 测试
test:
	$(GO) test -v -race -coverprofile=coverage.out ./...

# 测试覆盖率报告
test-cover: test
	$(GO) tool cover -html=coverage.out -o coverage.html

# 集成测试
test-integration:
	$(GO) test -v -tags=integration ./test/...

# 阶段一验证
verify:
	$(GO) run test/verify_stage1.go

# 清理
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html
	rm -rf logs/

# 交叉编译
build-linux:
	GOOS=linux GOARCH=amd64 $(GO) build -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 .

build-mac:
	GOOS=darwin GOARCH=amd64 $(GO) build -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 .

build-windows:
	GOOS=windows GOARCH=amd64 $(GO) build -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe .

# 帮助
help:
	@echo "可用目标:"
	@echo "  deps         - 安装依赖"
	@echo "  fmt          - 格式化代码"
	@echo "  lint         - 代码检查"
	@echo "  build        - 编译项目"
	@echo "  run          - 启动服务"
	@echo "  test         - 运行测试"
	@echo "  test-cover   - 生成测试覆盖率报告"
	@echo "  clean        - 清理构建产物"
	@echo "  verify       - 验证阶段一功能"
