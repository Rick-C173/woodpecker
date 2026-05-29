# Woodpecker AI 代码审查助手

## 项目简介
Woodpecker 是一个基于 LLM 的智能代码审查助手，可自动分析 GitHub PR 的代码变更，提供专业的审查意见。

## 功能特性
- ✅ 支持多种 LLM 提供商（OpenAI / DeepSeek / Claude / Ollama）
- ✅ 自动解析 Git diff，提取结构化变更
- ✅ 多维度审查（bug / security / performance / style / suggestion）
- ✅ GitHub Webhook 集成，自动触发 PR 审查
- ✅ 在 PR 上直接提交评论，支持代码建议块
- ✅ 可配置的审查规则和忽略模式
- ✅ 详细的审查报告和统计信息

## 快速开始

### 1. 环境要求
- Go 1.26+
- Git
- LLM API Key（可选，不配置则使用 Mock）

### 2. 配置
复制并编辑 `config.yaml`：
```bash
cp config.yaml.example config.yaml
```

### 3. 安装依赖
```bash
make deps
```

### 4. 启动服务
```bash
make run
```

### 5. 测试 API
```bash
curl -X POST http://localhost:8080/api/v1/review \
  -H "Content-Type: application/json" \
  -d '{"diff": "你的 git diff 文本", "language": "go"}'
```

## 配置说明

### 配置文件 (config.yaml)
```yaml
server:
  host: "0.0.0.0"
  port: 8080
  mode: debug

llm:
  provider: deepseek
  model: deepseek-chat
  base_url: https://api.deepseek.com/v1
  api_key: ""  # 从环境变量 WOODPECKER_LLM_API_KEY 读取
  max_tokens: 4096
  timeout: 60

github:
  app_id: ""      # GitHub App ID
  private_key: "" # GitHub App 私钥路径
  webhook_secret: "" # Webhook 签名密钥
  api_url: "https://api.github.com"

review:
  max_files_per_request: 20
  max_diff_chars: 50000
  default_language: go
  ignore_patterns:
    - "*.lock"
    - "*.sum"
    - "vendor/**"
    - "node_modules/**"
```

### 环境变量
```bash
export WOODPECKER_LLM_API_KEY="sk-xxx"
export WOODPECKER_GITHUB_TOKEN="ghp_xxx"
export WOODPECKER_WEBHOOK_SECRET="your-secret"
```

## GitHub 集成

### 1. 创建 GitHub App
1. 访问 https://github.com/settings/apps
2. 点击 "New GitHub App"
3. 配置：
   - Webhook URL: `https://your-domain.com/webhook`
   - Webhook secret: 与配置中的 `github.webhook_secret` 一致
   - 权限: Pull Requests (Read & Write)
   - 事件: Pull Request

### 2. 安装 App
1. 在 App 设置页面生成私钥
2. 将私钥保存到本地，配置 `github.private_key` 路径
3. 安装 App 到你的仓库

### 3. 配置 Webhook
服务启动后，GitHub PR 的打开/更新会自动触发审查。

## 项目结构

```
woodpecker/
├── main.go                           # 应用入口，组件初始化和服务启动
├── config.yaml                       # 配置文件
├── config.yaml.example               # 配置示例
├── CODE_WIKI.md                      # 完整的代码文档（详细架构说明）
├── Makefile                          # 构建和运行脚本
├── go.mod                            # Go 依赖管理
├── config/                           # 配置模块（公开）
├── internal/                         # 私有核心代码
│   ├── model/                        # 数据模型定义
│   ├── engine/                      # 核心引擎
│   │   ├── diff/                    # Diff 解析引擎
│   │   └── llm/                     # LLM 集成引擎
│   ├── git/                         # Git 操作封装
│   ├── github/                      # GitHub 集成
│   ├── pipeline/                    # 审查流水线
│   ├── service/                     # 业务服务层
│   └── handler/                     # HTTP 处理器层
├── pkg/                             # 可复用工具库
│   └── logger/                      # 结构化日志库
└── test/                            # 测试脚本
```

### 模块职责总览

| 模块 | 目录 | 主要职责 |
|------|------|----------|
| 入口 | `main.go` | 应用启动、配置加载、组件初始化 |
| 配置 | `config/` | YAML 配置加载、环境变量覆盖 |
| 模型 | `internal/model/` | 核心数据结构定义 |
| Diff解析 | `internal/engine/diff/` | Git diff 文本解析为结构化数据 |
| LLM引擎 | `internal/engine/llm/` | LLM API 调用、Prompt 构建、响应解析 |
| Git操作 | `internal/git/` | Git 命令封装（克隆、Diff、Fetch） |
| GitHub | `internal/github/` | GitHub API 操作、Webhook 处理 |
| 流水线 | `internal/pipeline/` | PR 审查完整流程编排 |
| 服务层 | `internal/service/` | 业务逻辑编排（Diff解析 → LLM调用 → 结果聚合） |
| 处理器 | `internal/handler/` | HTTP API 和 Webhook 路由处理 |
| 日志 | `pkg/logger/` | 结构化日志记录 |



### 添加新的审查规则

1. 修改 `internal/engine/llm/prompt.go` 中的 `reviewPromptTemplate` 提示词模板
2. 在 `internal/model/model.go` 中扩展 `ReviewComment` 结构体添加新字段
3. 更新 `internal/engine/llm/parser.go` 中的解析逻辑以支持新的输出格式

## 测试

### 单元测试
```bash
make test
```

### 集成测试
```bash
make test-integration
```

### 端到端测试
```bash
make test-e2e
```

## 部署

### Docker
```bash
docker build -t woodpecker .
docker run -p 8080:8080 woodpecker
```

### Kubernetes
```yaml
# 参考 k8s/woodpecker.yaml
```

## API 文档

### 健康检查
```
GET /health
```

### 代码审查
```
POST /api/v1/review
Content-Type: application/json

{
  "diff": "git diff 文本",
  "language": "go"
}
```

### GitHub Webhook
```
POST /webhook
X-GitHub-Event: pull_request
X-Hub-Signature-256: sha256=...
```

## 性能指标
- 单次审查平均耗时: 2-5 秒（取决于 diff 大小和 LLM 响应）
- 支持并发审查: 10 个 PR/秒
- 内存占用: ~100 MB

## 监控
- 内置 Prometheus metrics: `/metrics`
- 健康检查: `/health`
- 日志级别: 可通过配置调整

## 故障排除

### 常见问题
1. **LLM API 调用失败**
   - 检查 API Key 配置
   - 检查网络连接
   - 查看日志中的错误详情

2. **GitHub Webhook 不触发**
   - 验证 Webhook URL 可访问
   - 检查签名密钥匹配
   - 查看 GitHub 的 Webhook 交付记录

3. **Diff 解析失败**
   - 确保 diff 格式正确
   - 检查文件编码
   - 查看解析器日志

### 日志查看
```bash
tail -f logs/woodpecker.log
```

## 贡献指南
1. Fork 项目
2. 创建功能分支
3. 提交代码
4. 创建 Pull Request

## 许可证
Apache-2.0
