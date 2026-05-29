package llm

import (
	"context"
	"fmt"
	"strings"
	"woodpecker/internal/model"
)

// MockClient 返回预定义结果的模拟 LLM 客户端
// 用于：单元测试、本地开发、CI 流水线（不消耗 API 费用）
type MockClient struct {
	// 可配置：返回固定的评论列表
	FixedComments []model.ReviewComment
	// 可配置：模拟延迟（毫秒）
	DelayMs int
}

// NewMockClient 创建默认的 Mock 客户端
func NewMockClient() *MockClient {
	return &MockClient{
		FixedComments: defaultMockComments(),
		DelayMs:       100, // 默认 100ms 模拟网络延迟
	}
}

// NewMockClientWithComments 用自定义评论创建 Mock
func NewMockClientWithComments(comments []model.ReviewComment) *MockClient {
	return &MockClient{
		FixedComments: comments,
		DelayMs:       0,
	}
}

func (m *MockClient) Review(ctx context.Context, req ReviewRequest) (*ReviewResponse, error) {
	// 模拟网络延迟（可选）
	// time.Sleep(time.Duration(m.DelayMs) * time.Millisecond)

	// 根据请求内容生成"智能"的模拟回复
	comments := m.generateComments(req)

	summary := fmt.Sprintf("Mock 审查完成：检查了 %d 个文件，发现 %d 个问题",
		len(req.FileDiffs), len(comments))

	return &ReviewResponse{
		Comments:  comments,
		Summary:   summary,
		RawOutput: "mock-raw-output",
	}, nil
}

// Chat 模拟聊天接口
func (m *MockClient) Chat(ctx context.Context, prompt string) (string, error) {
	return "这是一个模拟的回答。在真实环境中，这里会调用 LLM API 基于代码库内容生成回答。", nil
}

// generateComments 根据 diff 内容生成相关评论（比完全固定更有测试价值）
func (m *MockClient) generateComments(req ReviewRequest) []model.ReviewComment {
	var comments []model.ReviewComment

	for _, fd := range req.FileDiffs {
		// 检查文件名包含 "test" 但没有测试用例
		if strings.Contains(fd.NewPath, "_test.go") && len(fd.Hunks) == 0 {
			comments = append(comments, model.ReviewComment{
				FilePath:   fd.NewPath,
				Line:       1,
				Category:   "style",
				Severity:   "info",
				Message:    "测试文件为空，请添加测试用例",
				Confidence: 0.9,
			})
		}

		// 检查是否包含 fmt.Println（常见 bad practice）
		for _, hunk := range fd.Hunks {
			for _, line := range hunk.Lines {
				if line.Type == "+" && strings.Contains(line.Content, "fmt.Println") {
					comments = append(comments, model.ReviewComment{
						FilePath:   fd.NewPath,
						Line:       line.NewLineNo,
						Category:   "suggestion",
						Severity:   "warning",
						Message:    "建议使用结构化日志（log/slog 或 zap）替代 fmt.Println",
						Suggestion: strings.Replace(line.Content, "fmt.Println", "slog.Info", 1),
						Confidence: 0.85,
					})
				}

				// 检查未处理 error
				if line.Type == "+" && strings.Contains(line.Content, "err") &&
					!strings.Contains(line.Content, "if err") &&
					!strings.Contains(line.Content, "return") {
					comments = append(comments, model.ReviewComment{
						FilePath:   fd.NewPath,
						Line:       line.NewLineNo,
						Category:   "bug",
						Severity:   "critical",
						Message:    "检测到可能的未处理 error，请检查并处理",
						Confidence: 0.75,
					})
				}
			}
		}
	}

	// 如果没有生成任何评论，返回默认的
	if len(comments) == 0 {
		return m.FixedComments
	}

	return comments
}

// defaultMockComments 默认的模拟评论
func defaultMockComments() []model.ReviewComment {
	return []model.ReviewComment{
		{
			FilePath:   "main.go",
			Line:       42,
			Category:   "bug",
			Severity:   "critical",
			Message:    "未检查 error 返回值，可能导致静默失败",
			Suggestion: "if err != nil {\n    return fmt.Errorf(\"操作失败: %w\", err)\n}",
			Confidence: 0.95,
			RuleID:     "GO-E001",
		},
		{
			FilePath:   "service.go",
			Line:       88,
			Category:   "performance",
			Severity:   "warning",
			Message:    "循环内重复创建正则表达式，建议提取到包级变量",
			Suggestion: "var re = regexp.MustCompile(`\\d+`)",
			Confidence: 0.88,
			RuleID:     "GO-P003",
		},
	}
}
