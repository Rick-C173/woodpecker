package service

import (
	"context"
	"fmt"
	"time"

	"woodpecker/internal/engine/diff"
	"woodpecker/internal/engine/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/store"
)

type Reviewer struct {
	llmClient llm.LlmClient
	maxDiff   int
	language  string
	store     *store.Store
}

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

func NewReviewerWithStore(client llm.LlmClient, maxDiffChars int, language string, s *store.Store) *Reviewer {
	r := NewReviewer(client, maxDiffChars, language)
	r.store = s
	return r
}

type ReviewRequest struct {
	DiffText   string
	Language   string
	ProjectID  int
	PRNumber   int
	PRTitle    string
	PRURL      string
	CommitSHA  string
	Branch     string
	BaseBranch string
}

type ReviewResponse struct {
	ReviewID int                 `json:"review_id"`
	Result   *model.ReviewResult `json:"result,omitempty"`
	Error    string              `json:"error,omitempty"`
	Elapsed  string              `json:"elapsed"`
}

func (r *Reviewer) Review(ctx context.Context, req ReviewRequest) *ReviewResponse {
	start := time.Now()

	fileDiffs, err := diff.Parse(req.DiffText)
	if err != nil {
		return &ReviewResponse{
			Error:   fmt.Sprintf("解析 diff 失败: %v", err),
			Elapsed: time.Since(start).String(),
		}
	}

	totalChars := totalDiffChars(fileDiffs)
	if totalChars > r.maxDiff {
		return &ReviewResponse{
			Error:   fmt.Sprintf("diff 过大 (%d 字符)，超过限制 (%d 字符)", totalChars, r.maxDiff),
			Elapsed: time.Since(start).String(),
		}
	}

	language := req.Language
	if language == "" {
		language = r.language
	}

	review := &store.Review{
		ProjectID:  req.ProjectID,
		PRNumber:   req.PRNumber,
		PRTitle:    req.PRTitle,
		PRURL:      req.PRURL,
		CommitSHA:  req.CommitSHA,
		Branch:     req.Branch,
		BaseBranch: req.BaseBranch,
		DiffText:   req.DiffText,
		Language:   language,
		Status:     "pending",
	}

	if r.store != nil {
		id, err := r.store.Reviews.SaveReview(ctx, review)
		if err != nil {
			return &ReviewResponse{
				Error:   fmt.Sprintf("保存审查记录失败: %v", err),
				Elapsed: time.Since(start).String(),
			}
		}
		review.ID = id
	}

	llmReq := llm.ReviewRequest{
		FileDiffs: fileDiffs,
		Language:  language,
	}

	llmResp, err := r.llmClient.Review(ctx, llmReq)
	if err != nil {
		if r.store != nil {
			review.Status = "failed"
			review.ErrorMessage = err.Error()
			r.store.Reviews.UpdateReview(ctx, review.ID, review)
		}
		return &ReviewResponse{
			ReviewID: review.ID,
			Error:    fmt.Sprintf("LLM 调用失败: %v", err),
			Elapsed:  time.Since(start).String(),
		}
	}

	result := buildReviewResult(llmResp, len(fileDiffs))

	if r.store != nil {
		review.Summary = llmResp.Summary
		review.TotalFiles = result.Stats.TotalFiles
		review.TotalIssues = result.Stats.TotalIssues
		review.Status = "success"

		if err := r.store.Reviews.UpdateReview(ctx, review.ID, review); err != nil {
			return &ReviewResponse{
				ReviewID: review.ID,
				Result:   result,
				Error:    fmt.Sprintf("更新审查记录失败: %v", err),
				Elapsed:  time.Since(start).String(),
			}
		}

		for _, c := range result.Comments {
			if err := r.store.Reviews.SaveComment(ctx, review.ID, &c); err != nil {
				return &ReviewResponse{
					ReviewID: review.ID,
					Result:   result,
					Error:    fmt.Sprintf("保存评论失败: %v", err),
					Elapsed:  time.Since(start).String(),
				}
			}
		}
	}

	return &ReviewResponse{
		ReviewID: review.ID,
		Result:   result,
		Elapsed:  time.Since(start).String(),
	}
}

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

func totalDiffChars(files []model.FileDiff) int {
	total := 0
	for _, f := range files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				total += len(l.Content) + 1
			}
		}
	}
	return total
}
