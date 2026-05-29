package llm

import (
	"context"

	"woodpecker/internal/model"
)

// LlmClient LLM 客户端统一接口
type LlmClient interface {
	Review(ctx context.Context, req ReviewRequest) (*ReviewResponse, error)
	Chat(ctx context.Context, prompt string) (string, error)
}

// ReviewRequest LLM 审查请求
type ReviewRequest struct {
	FileDiffs []model.FileDiff // 待审查的文件变更
	Language  string           // 编程语言
	Context   string           // 额外上下文（如 PR 描述）
}

// ReviewResponse LLM 审查响应
type ReviewResponse struct {
	Comments  []model.ReviewComment // 审查评论列表
	Summary   string                // 整体总结
	RawOutput string                // LLM 原始输出（调试用）
}
