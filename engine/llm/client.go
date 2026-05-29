package llm

import (
	"context"
	"woodpecker/po"
)

// LlmClient 是 LLM 调用的抽象接口
type LlmClient interface {
	// Review 发送代码 diff 给 LLM，返回审查结果
	Review(ctx context.Context, req ReviewRequest) (*ReviewResponse, error)
}

// ReviewRequest 审查请求
type ReviewRequest struct {
	FileDiffs []po.FileDiff // 要审查的文件变更列表
	Language  string        // 编程语言（go, python, java...）
	Context   string        // 额外上下文（如团队规范提示）
}

// ReviewResponse LLM 返回的审查结果
type ReviewResponse struct {
	Comments  []po.ReviewComment // AI 给出的审查意见
	Summary   string             // 整体总结
	RawOutput string             // LLM 原始输出（用于调试）
}
