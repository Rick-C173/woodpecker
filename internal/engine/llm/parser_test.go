package llm

import (
	"encoding/json"
	"testing"

	"woodpecker/internal/model"
)

func TestParseReviewResponse(t *testing.T) {
	tests := []struct {
		name         string
		rawJSON      string
		wantErr      bool
		wantComments int
		wantSeverity string
	}{
		{
			name: "正常 JSON",
			rawJSON: `{
				"comments": [
					{
						"file_path": "main.go",
						"line": 42,
						"category": "bug",
						"severity": "critical",
						"message": "未检查 error 返回值",
						"suggestion": "if err != nil { return err }",
						"confidence": 0.95,
						"rule_id": "GO-E001"
					}
				],
				"summary": "发现1个严重问题"
			}`,
			wantErr:      false,
			wantComments: 1,
			wantSeverity: "critical",
		},
		{
			name:         "带 markdown 代码块包裹",
			rawJSON:      "```json\n{\n  \"comments\": [\n    {\n      \"file_path\": \"\",\n      \"line\": 0,\n      \"category\": \"style\",\n      \"severity\": \"info\",\n      \"message\": \"代码格式良好\",\n      \"suggestion\": \"\",\n      \"confidence\": 0.8,\n      \"rule_id\": \"\"\n    }\n  ],\n  \"summary\": \"通过\"\n}\n```",
			wantErr:      false,
			wantComments: 1,
		},
		{
			name:         "空 JSON",
			rawJSON:      `{}`,
			wantErr:      false,
			wantComments: 0,
		},
		{
			name:    "无效 JSON",
			rawJSON: `{invalid`,
			wantErr: true,
		},
		{
			name: "无评论",
			rawJSON: `{
				"comments": [],
				"summary": "无问题"
			}`,
			wantErr:      false,
			wantComments: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := ParseReviewResponse(tt.rawJSON)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseReviewResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if len(resp.Comments) != tt.wantComments {
				t.Errorf("got %d comments, want %d", len(resp.Comments), tt.wantComments)
			}
			if tt.wantSeverity != "" && len(resp.Comments) > 0 {
				if resp.Comments[0].Severity != tt.wantSeverity {
					t.Errorf("severity = %s, want %s", resp.Comments[0].Severity, tt.wantSeverity)
				}
			}
		})
	}
}

func TestCleanJSON(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "```json\n{\"key\": \"value\"}\n```",
			expected: "{\"key\": \"value\"}",
		},
		{
			input:    "```\n{\"key\": \"value\"}\n```",
			expected: "{\"key\": \"value\"}",
		},
		{
			input:    "  {\"key\": \"value\"}  ",
			expected: "{\"key\": \"value\"}",
		},
		{
			input:    "{\"key\": \"value\"}",
			expected: "{\"key\": \"value\"}",
		},
	}

	for _, tt := range tests {
		got := cleanJSON(tt.input)
		if got != tt.expected {
			t.Errorf("cleanJSON(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeCategory(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"bug", "bug"},
		{"BUG", "bug"},
		{"  security  ", "security"},
		{"invalid", "suggestion"},
		{"", "suggestion"},
	}

	for _, tt := range tests {
		got := normalizeCategory(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeCategory(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"critical", "critical"},
		{"CRITICAL", "critical"},
		{"  warning  ", "warning"},
		{"unknown", "info"},
		{"", "info"},
	}

	for _, tt := range tests {
		got := normalizeSeverity(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeSeverity(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// 确保 ReviewResponse 可以 JSON 序列化
func TestReviewResponse_JSONRoundtrip(t *testing.T) {
	original := ReviewResponse{
		Comments: []model.ReviewComment{
			{
				FilePath:   "main.go",
				Line:       10,
				Category:   "bug",
				Severity:   "critical",
				Message:    "test",
				Confidence: 0.9,
			},
		},
		Summary:   "测试总结",
		RawOutput: `{"test": true}`,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded ReviewResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(decoded.Comments) != len(original.Comments) {
		t.Errorf("comments count mismatch: %d != %d", len(decoded.Comments), len(original.Comments))
	}
	if decoded.Summary != original.Summary {
		t.Errorf("summary mismatch: %s != %s", decoded.Summary, original.Summary)
	}
}
