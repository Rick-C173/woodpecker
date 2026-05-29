// internal/engine/llm/mock_test.go
package main

import (
	"context"
	"strings"
	"testing"
	"woodpecker/engine/llm"
	"woodpecker/po"
)

func TestMockClient_Review(t *testing.T) {
	client := llm.NewMockClient()
	ctx := context.Background()

	req := llm.ReviewRequest{
		Language: "Go",
		FileDiffs: []po.FileDiff{
			{
				OldPath: "main.go",
				NewPath: "main.go",
				Status:  "modified",
				Hunks: []po.Hunk{
					{
						OldStart: 1, NewStart: 1,
						Lines: []po.Line{
							{Type: "+", Content: `fmt.Println("debug")`, NewLineNo: 5},
						},
					},
				},
			},
		},
	}

	resp, err := client.Review(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 验证返回了评论
	if len(resp.Comments) == 0 {
		t.Error("expected comments, got none")
	}

	// 验证检测到 fmt.Println
	found := false
	for _, c := range resp.Comments {
		if c.Category == "suggestion" && c.Line == 5 {
			found = true
			break
		}
	}
	if !found {
		t.Logf("comments: %+v", resp.Comments)
		t.Error("expected suggestion for fmt.Println")
	}

	// 验证 Summary
	if resp.Summary == "" {
		t.Error("expected non-empty summary")
	}

	t.Logf("Summary: %s", resp.Summary)
	t.Logf("Comments count: %d", len(resp.Comments))
}

func TestPromptBuilder_Build(t *testing.T) {
	pb, err := llm.NewPromptBuilder()
	if err != nil {
		t.Fatalf("failed to create prompt builder: %v", err)
	}

	req := llm.ReviewRequest{
		Language: "Go",
		Context:  "这是一个 API 服务，使用 Gin 框架",
		FileDiffs: []po.FileDiff{
			{
				OldPath: "handler.go",
				NewPath: "handler.go",
				Hunks: []po.Hunk{
					{
						OldStart: 10, OldLines: 3,
						NewStart: 10, NewLines: 5,
						Lines: []po.Line{
							{Type: " ", Content: "func GetUser(c *gin.Context) {", NewLineNo: 10},
							{Type: "+", Content: "    id := c.Param(\"id\")", NewLineNo: 11},
							{Type: "+", Content: "    user, _ := db.GetUser(id)", NewLineNo: 12},
						},
					},
				},
			},
		},
	}

	prompt, err := pb.Build(req)
	if err != nil {
		t.Fatalf("failed to build prompt: %v", err)
	}

	// 验证 prompt 包含关键信息
	checks := []string{
		"Go", "代码审查", "handler.go", "GetUser",
		"bug", "security", "performance", "JSON",
	}

	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("prompt missing: %q", check)
		}
	}

	t.Logf("Prompt length: %d", len(prompt))
	t.Logf("Prompt preview:\n%s", prompt[:min(500, len(prompt))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
