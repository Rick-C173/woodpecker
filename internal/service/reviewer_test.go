package service

import (
	"context"
	"testing"

	"woodpecker/internal/engine/llm"
	"woodpecker/internal/model"
)

func TestNewReviewer(t *testing.T) {
	client := llm.NewMockClient()
	reviewer := NewReviewer(client, 100000, "go")
	if reviewer == nil {
		t.Fatal("NewReviewer 返回nil")
	}
	if reviewer.maxDiff != 100000 {
		t.Errorf("maxDiff 期望 100000，实际 %d", reviewer.maxDiff)
	}
	if reviewer.language != "go" {
		t.Errorf("language 期望 go，实际 %s", reviewer.language)
	}
}

func TestNewReviewerWithDefaults(t *testing.T) {
	client := llm.NewMockClient()
	reviewer := NewReviewer(client, 0, "")
	if reviewer.maxDiff != 50000 {
		t.Errorf("默认 maxDiff 期望 50000，实际 %d", reviewer.maxDiff)
	}
	if reviewer.language != "go" {
		t.Errorf("默认 language 期望 go，实际 %s", reviewer.language)
	}
}

func TestNewReviewerWithStore(t *testing.T) {
	client := llm.NewMockClient()
	// 这里我们不需要真实的store，只要不为nil即可用于检查是否正确初始化
	reviewer := NewReviewerWithStore(client, 1000, "go", nil)
	if reviewer == nil {
		t.Fatal("NewReviewerWithStore 返回nil")
	}
	if reviewer.store != nil {
		t.Error("期望store为nil，实际不为nil")
	}
}

func TestReviewer_Review_NoStore(t *testing.T) {
	client := llm.NewMockClient()
	reviewer := NewReviewer(client, 100000, "go")

	req := ReviewRequest{
		DiffText: `diff --git a/test.go b/test.go
--- a/test.go
+++ b/test.go
@@ -1 +1 @@
-func main() {}
+func main() { fmt.Println("hello") }`,
	}

	resp := reviewer.Review(context.Background(), req)
	if resp == nil {
		t.Fatal("Review 返回nil")
	}
	if resp.Error != "" {
		t.Errorf("不期望有错误，实际有: %s", resp.Error)
	}
	if resp.Result == nil {
		t.Error("期望返回结果不应该为nil")
	}
	if resp.Result.Summary == "" {
		t.Error("期望有一个总结")
	}
	if resp.ReviewID != 0 {
		t.Error("没有数据库时，ReviewID应该为0")
	}
}

func TestReviewer_Review_InvalidDiff(t *testing.T) {
	client := llm.NewMockClient()
	reviewer := NewReviewer(client, 100000, "go")

	req := ReviewRequest{
		DiffText: "invalid diff",
	}

	resp := reviewer.Review(context.Background(), req)
	if resp == nil {
		t.Fatal("Review 返回nil")
	}
	if resp.Error == "" {
		t.Error("期望有错误，实际没有")
	}
	if resp.Result != nil {
		t.Error("有错误时，Result应该为nil")
	}
}

func TestReviewer_Review_DiffTooLarge(t *testing.T) {
	client := llm.NewMockClient()
	reviewer := NewReviewer(client, 10, "go") // 限制非常小

	// 制造一个大diff
	bigDiff := "diff --git a/big.go b/big.go\n"
	bigDiff += "--- a/big.go\n"
	bigDiff += "+++ b/big.go\n"
	bigDiff += "@@ -0,0 +1,10000 @@\n"
	bigDiff += "+some very long content"

	req := ReviewRequest{
		DiffText: bigDiff,
	}

	resp := reviewer.Review(context.Background(), req)
	if resp == nil {
		t.Fatal("Review 返回nil")
	}
	if resp.Error == "" {
		t.Error("期望有错误，实际没有")
	}
	if !contains(resp.Error, "过大") {
		t.Errorf("错误信息应该提到diff过大，实际是: %s", resp.Error)
	}
}

func TestReviewer_Review_Success(t *testing.T) {
	// 创建一个有预定义评论的mock client
	comments := []model.ReviewComment{
		{
			FilePath: "test.go",
			Line:     42,
			Category: "bug",
			Severity: "critical",
			Message:  "测试错误",
		},
	}
	client := llm.NewMockClientWithComments(comments)
	reviewer := NewReviewer(client, 100000, "go")

	req := ReviewRequest{
		DiffText: `diff --git a/test.go b/test.go
--- a/test.go
+++ b/test.go
@@ -1,3 +1,3 @@
 package test
`,
	}

	resp := reviewer.Review(context.Background(), req)
	if resp == nil {
		t.Fatal("Review 返回nil")
	}
	if resp.Error != "" {
		t.Errorf("不期望有错误，实际有: %s", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("Result 不应该为nil")
	}
	if len(resp.Result.Comments) == 0 {
		t.Error("期望有评论")
	}
	if resp.Result.Summary == "" {
		t.Errorf("期望Summary不为空")
	}
}

func TestReviewer_Review_LanguageOverride(t *testing.T) {
	client := llm.NewMockClient()
	reviewer := NewReviewer(client, 100000, "go")

	req := ReviewRequest{
		DiffText: `diff --git a/test.py b/test.py
--- a/test.py
+++ b/test.py
@@ -1,2 +1,2 @@
 def test(): pass
`,
		Language: "python",
	}

	resp := reviewer.Review(context.Background(), req)
	if resp == nil {
		t.Fatal("Review 返回nil")
	}
	if resp.Error != "" {
		t.Errorf("不期望有错误，实际有: %s", resp.Error)
	}
}

// 测试辅助函数
func contains(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// 我们无法在这里测试totalDiffChars
func TestTotalDiffChars(t *testing.T) {
	// 注意：这个函数没有导出，但我们可以间接测试它
	// 通过 Reviewer 的行为来验证
	client := llm.NewMockClient()
	reviewer := NewReviewer(client, 100, "go")

	// 制造一个已知大小的diff
	diff := `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,10 +1,10 @@
 func a() {
 }
`
	req := ReviewRequest{
		DiffText: diff,
	}
	resp := reviewer.Review(context.Background(), req)
	if resp == nil {
		t.Fatal("Review 返回nil")
	}
}
