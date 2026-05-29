package po

import "time"

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
