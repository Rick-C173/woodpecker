package model

import "time"

type FileDiff struct {
	// 文件路径
	OldPath string // 变更前的文件路径（如 "src/utils.go"）
	NewPath string // 变更后的文件路径（重命名时可能不同）

	// 变更类型
	Status string // "added" | "modified" | "deleted" | "renamed"

	// 具体变更内容
	Hunks []Hunk // 代码块列表（diff 被切分成多个片段）
}

type Hunk struct {
	OldStart int    // 旧文件起始行号
	OldLines int    // 旧文件涉及行数
	NewStart int    // 新文件起始行号
	NewLines int    // 新文件涉及行数
	Lines    []Line // 每一行的变更详情
}

type Line struct {
	Type      string // " "（未变）| "+"（新增）| "-"（删除）
	Content   string // 行内容
	OldLineNo int    // 在旧文件中的行号
	NewLineNo int    // 在新文件中的行号
}

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

type ReviewResult struct {
	// 基本信息
	PRID      int    // 关联的 Pull Request ID
	CommitSHA string // 审查时的代码版本

	// 聚合结果
	Comments []ReviewComment // 所有审查意见
	Summary  string          // AI 生成的整体总结（如"发现3个严重问题"）

	// 统计信息
	Stats struct {
		TotalFiles  int            // 审查了多少文件
		TotalIssues int            // 总共发现多少问题
		BySeverity  map[string]int // {"critical": 2, "warning": 5}
		ByCategory  map[string]int // {"security": 1, "style": 6}
	}

	// 成本信息
	TokenUsage struct {
		PromptTokens     int     // 输入 token 数
		CompletionTokens int     // 输出 token 数
		TotalCost        float64 // 估算费用（美元）
	}

	// 时间戳
	CreatedAt time.Time
}
