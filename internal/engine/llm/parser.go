package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"woodpecker/internal/model"
)

// llmOutput 对应 LLM 返回的 JSON 结构
type llmOutput struct {
	Comments []llmComment `json:"comments"`
	Summary  string       `json:"summary"`
}

type llmComment struct {
	FilePath   string  `json:"file_path"`
	Line       int     `json:"line"`
	Category   string  `json:"category"`
	Severity   string  `json:"severity"`
	Message    string  `json:"message"`
	Suggestion string  `json:"suggestion"`
	Confidence float64 `json:"confidence"`
	RuleID     string  `json:"rule_id"`
}

// ParseReviewResponse 解析 LLM 返回的原始 JSON 为 ReviewResponse
func ParseReviewResponse(rawJSON string) (*ReviewResponse, error) {
	cleaned := cleanJSON(rawJSON)

	var output llmOutput
	if err := json.Unmarshal([]byte(cleaned), &output); err != nil {
		return nil, fmt.Errorf("parse LLM response JSON: %w\nraw output: %s", err, truncate(rawJSON, 500))
	}

	comments := make([]model.ReviewComment, 0, len(output.Comments))
	for _, c := range output.Comments {
		comments = append(comments, model.ReviewComment{
			FilePath:   c.FilePath,
			Line:       c.Line,
			Category:   normalizeCategory(c.Category),
			Severity:   normalizeSeverity(c.Severity),
			Message:    c.Message,
			Suggestion: c.Suggestion,
			Confidence: c.Confidence,
			RuleID:     c.RuleID,
		})
	}

	return &ReviewResponse{
		Comments:  comments,
		Summary:   output.Summary,
		RawOutput: rawJSON,
	}, nil
}

// cleanJSON 去除 LLM 输出中可能包裹的 markdown 代码块标记
func cleanJSON(raw string) string {
	s := strings.TrimSpace(raw)

	// 去除 ```json ... ``` 包裹
	if strings.HasPrefix(s, "```") {
		// 找到第一个换行后的内容
		idx := strings.Index(s, "\n")
		if idx != -1 {
			s = s[idx+1:]
		}
		// 去除结尾的 ```
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}

	return s
}

// normalizeCategory 标准化分类字段
func normalizeCategory(c string) string {
	valid := map[string]bool{
		"bug": true, "security": true, "performance": true,
		"style": true, "suggestion": true,
	}
	lower := strings.ToLower(strings.TrimSpace(c))
	if valid[lower] {
		return lower
	}
	return "suggestion" // 默认归类为建议
}

// normalizeSeverity 标准化严重等级
func normalizeSeverity(s string) string {
	valid := map[string]bool{
		"critical": true, "warning": true, "info": true,
	}
	lower := strings.ToLower(strings.TrimSpace(s))
	if valid[lower] {
		return lower
	}
	return "info"
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
