package po

type ReviewComment struct {
	// 定位信息
	FilePath string // 哪个文件（如 "src/utils.go"）
	Line     int    // 新文件的行号（GitHub 评论需要）

	// 审查内容
	Category string // "bug" | "security" | "performance" | "style" | "suggestion"
	Severity string // "critical" | "warning" | "info"
	Message  string // AI 给出的具体建议（如"这里可能发生空指针 panic"）

	// 可选：建议的修复代码
	Suggestion string // 建议替换的代码片段

	// 元数据
	Confidence float64 // AI 置信度（0-1）
	RuleID     string  // 触发的规则编号（如 "GO-S1024"）
}
