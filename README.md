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

## 开发指南

### 项目结构
```
woodpecker/
├── main.go                        # 入口
├── config.yaml                    # 配置文件
├── Makefile / README.md
│
├── config/                        # 配置模块（公开）
│   └── config.go
│
├── internal/                      # 私有核心代码
│   ├── model/
│   │   └── model.go               # 合并：FileDiff/Hunk/Line + ReviewComment + ReviewResult
│   ├── engine/
│   │   ├── diff/
│   │   │   ├── parser.go
│   │   │   └── parser_test.go
│   │   └── llm/
│   │       ├── interface.go       # 补充：LlmClient 接口 + ReviewRequest/Response
│   │       ├── mock.go
│   │       ├── openai.go
│   │       ├── parser.go
│   │       ├── parser_test.go
│   │       └── prompt.go
│   ├── git/
│   │   └── executor.go
│   ├── github/
│   │   ├── client.go
│   │   └── webhook.go
│   ├── pipeline/
│   │   └── processor.go
│   ├── service/
│   │   ├── reviewer.go
│   │   └── reviewer_test.go
│   └── handler/
│       ├── review.go
│       └── webhook.go
│
├── pkg/
│   └── logger/                    # 可复用日志库
│       └── logger.go
│
└── test/
    ├── sample.diff
    ├── verify_stage1.go
    └── verify_stage23.go
```

### 添加新的 LLM 提供商
1. 在 `engine/llm/` 下创建新的客户端，实现 `LlmClient` 接口
2. 在 `config.go` 的 `LLMConfig` 中添加相应配置
3. 在 `main.go` 的客户端初始化中添加支持

### 添加新的审查规则
1. 修改 `engine/llm/prompt.go` 中的提示词模板
2. 在 `po/reviewComment.go` 中添加新的分类/严重等级
3. 更新解析器以支持新的输出格式

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
