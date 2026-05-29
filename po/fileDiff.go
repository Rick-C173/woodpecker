package po

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
