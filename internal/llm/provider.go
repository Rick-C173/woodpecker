// Package llm Provider 抽象（技术选型 §8）：Chat 流式 / Embed 批量 / Rerank。
// 实现面向 OpenAI 兼容 API；Rerank 走 SiliconFlow/Jina 风格 /v1/rerank 端点。
// 配置随调用注入（管理员改默认配置后新请求立即生效，PRD FR-12）。
package llm

import "context"

// 用途常量。
const (
	PurposeChat      = "chat"
	PurposeEmbedding = "embedding"
	PurposeRerank    = "rerank"
)

// ResolvedConfig 一条可用的模型配置（service 层解密后注入）。
type ResolvedConfig struct {
	BaseURL   string
	APIKey    string
	ModelName string
	Params    string // 原始 JSON 参数（temperature 等），实现自行解析
}

// ChatDelta 流式增量。
type ChatDelta struct {
	Content string
}

// ChatRequest 对话请求。
type ChatRequest struct {
	System string
	Prompt string
}

// ChatStream 发起流式对话；返回增量通道与首错（连接失败立即返回错误）。
// 通道关闭表示生成结束；ctx 取消时实现须停止请求并关闭通道。
type ChatStream func(ctx context.Context, cfg ResolvedConfig, req ChatRequest) (<-chan ChatDelta, error)

// Embed 批量向量化，返回与 texts 等长的向量切片。
type Embed func(ctx context.Context, cfg ResolvedConfig, texts []string) ([][]float32, error)

// Rerank 按相关度重排：返回与 docs 等长的分数切片。
type Rerank func(ctx context.Context, cfg ResolvedConfig, query string, docs []string) ([]float64, error)

// Provider 三能力聚合。
type Provider interface {
	ChatStream
	Embed
	Rerank
}

// Tester 逐项连通性测试（测试连接端点用）。
type Tester interface {
	TestChat(ctx context.Context, cfg ResolvedConfig) error
	TestEmbedding(ctx context.Context, cfg ResolvedConfig) error
	TestRerank(ctx context.Context, cfg ResolvedConfig) error
}
