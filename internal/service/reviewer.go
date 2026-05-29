package service

import (
	"context"
	"fmt"
	"time"

	"woodpecker/internal/engine/diff"
	"woodpecker/internal/engine/llm"
	"woodpecker/internal/model"
)

// Reviewer 代码审查核心服务，串联 diff 解析 → LLM 调用 → 结果聚合
type Reviewer struct {
	llmClient llm.LlmClient
	maxDiff   int    // 单次最大 diff 字符数
	language  string // 默认编程语言
}

// NewReviewer 创建审查服务
func NewReviewer(client llm.LlmClient, maxDiffChars int, language string) *Reviewer {
	if maxDiffChars <= 0 {
		maxDiffChars = 50000
	}
	if language == "" {
		language = "go"
	}
	return &Reviewer{
		llmClient: client,
		maxDiff:   maxDiffChars,
		language:  language,
	}
}

// ReviewRequest 上层审查请求（HTTP 层传入）
type ReviewRequest struct {
	DiffText string // 原始 git diff 文本
	Language string // 编程语言，空则用默认值
}

// ReviewResponse 上层审查响应
type ReviewResponse struct {
	Result  *model.ReviewResult // 聚合后的审查结果
	Error   string              // 错误信息（如有）
	Elapsed string              // 耗时
}

// Review 执行完整的代码审查流程
func (r *Reviewer) Review(ctx context.Context, req ReviewRequest) *ReviewResponse {
	start := time.Now()

	// 1. 解析 diff 文本
	fileDiffs, err := diff.Parse(req.DiffText)
	if err != nil {
		return &ReviewResponse{
			Error:   fmt.Sprintf("解析 diff 失败: %v", err),
			Elapsed: time.Since(start).String(),
		}
	}

	// 2. 检查 diff 大小
	totalChars := totalDiffChars(fileDiffs)
	if totalChars > r.maxDiff {
		return &ReviewResponse{
			Error:   fmt.Sprintf("diff 过大 (%d 字符)，超过限制 (%d 字符)，请缩小审查范围", totalChars, r.maxDiff),
			Elapsed: time.Since(start).String(),
		}
	}

	// 3. 确定语言
	language := req.Language
	if language == "" {
		language = r.language
	}

	// 4. 调用 LLM
	llmReq := llm.ReviewRequest{
		FileDiffs: fileDiffs,
		Language:  language,
	}

	llmResp, err := r.llmClient.Review(ctx, llmReq)
	if err != nil {
		return &ReviewResponse{
			Error:   fmt.Sprintf("LLM 调用失败: %v", err),
			Elapsed: time.Since(start).String(),
		}
	}

	// 5. 构建聚合结果
	result := buildReviewResult(llmResp, len(fileDiffs))

	return &ReviewResponse{
		Result:  result,
		Elapsed: time.Since(start).String(),
	}
}

// buildReviewResult 将 LLM 响应转换为标准 ReviewResult
func buildReviewResult(llmResp *llm.ReviewResponse, totalFiles int) *model.ReviewResult {
	comments := llmResp.Comments
	if comments == nil {
		comments = []model.ReviewComment{}
	}

	stats := struct {
		TotalFiles  int
		TotalIssues int
		BySeverity  map[string]int
		ByCategory  map[string]int
	}{
		TotalFiles:  totalFiles,
		TotalIssues: len(comments),
		BySeverity:  make(map[string]int),
		ByCategory:  make(map[string]int),
	}

	for _, c := range comments {
		stats.BySeverity[c.Severity]++
		stats.ByCategory[c.Category]++
	}

	return &model.ReviewResult{
		Comments:  comments,
		Summary:   llmResp.Summary,
		Stats:     stats,
		CreatedAt: time.Now(),
	}
}

// totalDiffChars 计算所有文件 diff 的总字符数
func totalDiffChars(files []model.FileDiff) int {
	total := 0
	for _, f := range files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				total += len(l.Content) + 1 // +1 for the type prefix
			}
		}
	}
	return total
}
