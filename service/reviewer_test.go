package service

import (
	"context"
	"testing"

	"woodpecker/engine/llm"
	"woodpecker/po"
)

// mockReviewClient 用于测试的 Mock LLM 客户端
type mockReviewClient struct {
	comments []po.ReviewComment
	summary  string
	err      error
}

func (m *mockReviewClient) Review(ctx context.Context, req llm.ReviewRequest) (*llm.ReviewResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ReviewResponse{
		Comments:  m.comments,
		Summary:   m.summary,
		RawOutput: "mock",
	}, nil
}

func TestReviewer_Success(t *testing.T) {
	client := &mockReviewClient{
		comments: []po.ReviewComment{
			{
				FilePath:   "main.go",
				Line:       10,
				Category:   "bug",
				Severity:   "critical",
				Message:    "空指针风险",
				Confidence: 0.95,
			},
			{
				FilePath:   "utils.go",
				Line:       20,
				Category:   "style",
				Severity:   "info",
				Message:    "变量命名不规范",
				Confidence: 0.8,
			},
		},
		summary: "发现2个问题",
	}

	reviewer := NewReviewer(client, 50000, "go")

	const diff = `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,3 +1,4 @@
 package main
 func main() {
+	fmt.Println("hello")
 }`

	req := ReviewRequest{
		DiffText: diff,
		Language: "go",
	}

	resp := reviewer.Review(nil, req)
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("expected non-nil result")
	}
	if resp.Result.Stats.TotalIssues != 2 {
		t.Errorf("expected 2 issues, got %d", resp.Result.Stats.TotalIssues)
	}
	if resp.Result.Stats.TotalFiles != 1 {
		t.Errorf("expected 1 file, got %d", resp.Result.Stats.TotalFiles)
	}

	// 验证统计分布
	if resp.Result.Stats.BySeverity["critical"] != 1 {
		t.Errorf("expected 1 critical, got %d", resp.Result.Stats.BySeverity["critical"])
	}
	if resp.Result.Stats.BySeverity["info"] != 1 {
		t.Errorf("expected 1 info, got %d", resp.Result.Stats.BySeverity["info"])
	}
	if resp.Result.Stats.ByCategory["bug"] != 1 {
		t.Errorf("expected 1 bug, got %d", resp.Result.Stats.ByCategory["bug"])
	}
}

func TestReviewer_EmptyDiff(t *testing.T) {
	client := &mockReviewClient{}
	reviewer := NewReviewer(client, 50000, "go")

	req := ReviewRequest{
		DiffText: "",
		Language: "go",
	}

	resp := reviewer.Review(nil, req)
	if resp.Error == "" {
		t.Error("expected error for empty diff")
	}
}

func TestReviewer_InvalidDiff(t *testing.T) {
	client := &mockReviewClient{}
	reviewer := NewReviewer(client, 50000, "go")

	req := ReviewRequest{
		DiffText: "not a valid diff",
		Language: "go",
	}

	resp := reviewer.Review(nil, req)
	if resp.Error == "" {
		t.Error("expected error for invalid diff")
	}
}

func TestReviewer_DiffTooLarge(t *testing.T) {
	client := &mockReviewClient{}
	// 限制 10 字符
	reviewer := NewReviewer(client, 10, "go")

	const diff = `diff --git a/large.go b/large.go
--- a/large.go
+++ b/large.go
@@ -1,1 +1,50 @@
+very long content exceeds limit`

	req := ReviewRequest{
		DiffText: diff,
		Language: "go",
	}

	resp := reviewer.Review(nil, req)
	if resp.Error == "" {
		t.Error("expected error for oversized diff")
	}
}

func TestReviewer_ZeroComments(t *testing.T) {
	client := &mockReviewClient{
		comments: []po.ReviewComment{},
		summary:  "无问题",
	}

	reviewer := NewReviewer(client, 50000, "go")

	const diff = `diff --git a/clean.go b/clean.go
--- a/clean.go
+++ b/clean.go
@@ -1,1 +1,2 @@
 package main
+// good code`

	req := ReviewRequest{
		DiffText: diff,
		Language: "go",
	}

	resp := reviewer.Review(nil, req)
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Result.Stats.TotalIssues != 0 {
		t.Errorf("expected 0 issues for clean code, got %d", resp.Result.Stats.TotalIssues)
	}
}

func TestNewReviewer_Defaults(t *testing.T) {
	// 测试默认值
	r := NewReviewer(nil, 0, "")
	if r.maxDiff != 50000 {
		t.Errorf("expected default maxDiff=50000, got %d", r.maxDiff)
	}
	if r.language != "go" {
		t.Errorf("expected default language=go, got %s", r.language)
	}
}
