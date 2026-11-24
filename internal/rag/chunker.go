package rag

import (
	"strings"
	"unicode/utf8"
)

// 切块参数（技术选型 §6.2；M5 调参后可调整）。
const (
	maxChunkRunes  = 800 // 单块上限（rune）
	minChunkRunes  = 80  // 小于该值的段落并入相邻块
	overlapRunes   = 100 // 递归字符切块的重叠窗口
	maxHeadingPath = 6   // 标题路径最大层级
)

// ChunkBlock 切块结果。
type ChunkBlock struct {
	Content     string // 块文本（可含标题路径前缀）
	HeadingPath string // MD 标题路径，如 "部署 / 配置"；代码/文本为空
	LineStart   int    // 1 起始行号；0 表示无行号语义
	LineEnd     int
}

// Chunk 按内容类型分流切块：md → 标题层级；代码 → 函数边界启发式；其余 → 递归字符。
// kind: "markdown" | "code" | "text"。
func Chunk(kind, content string) []ChunkBlock {
	switch kind {
	case "markdown":
		blocks := chunkMarkdown(content)
		if len(blocks) > 0 {
			return blocks
		}
		return chunkText(content) // 空结构 MD 退化为通用切块
	case "code":
		blocks := chunkCode(content)
		if len(blocks) > 0 {
			return blocks
		}
		return chunkText(content)
	default:
		return chunkText(content)
	}
}

// ---- Markdown：按标题层级切块，超长段落二次切分 ----

// chunkMarkdown 解析 ATX 标题（# ~ ######）构建标题路径，同一标题下的内容为一块。
func chunkMarkdown(content string) []ChunkBlock {
	type sec struct {
		heading string // 完整标题路径
		start   int    // 起始行（含标题行）
		body    strings.Builder
	}
	var blocks []ChunkBlock
	var stack []string // 当前标题栈
	var cur *sec
	flush := func() {
		if cur == nil {
			return
		}
		text := strings.TrimSpace(cur.body.String())
		if text != "" {
			blocks = append(blocks, ChunkBlock{
				Content:     text,
				HeadingPath: cur.heading,
				LineStart:   cur.start,
			})
		}
		cur = nil
	}

	lines := strings.Split(content, "\n")
	inCodeFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeFence = !inCodeFence
		}
		// 标题行（代码围栏内不算）
		if !inCodeFence && isHeading(trimmed) {
			flush()
			level := headingLevel(trimmed)
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if level <= len(stack) {
				stack = stack[:level-1]
			}
			stack = append(stack, title)
			if len(stack) > maxHeadingPath {
				stack = stack[len(stack)-maxHeadingPath:]
			}
			cur = &sec{heading: strings.Join(stack, " / "), start: i + 1}
			continue
		}
		if cur == nil {
			cur = &sec{start: i + 1}
		}
		cur.body.WriteString(line)
		cur.body.WriteString("\n")
	}
	flush()

	return finishBlocks(blocks)
}

func isHeading(line string) bool {
	if !strings.HasPrefix(line, "#") {
		return false
	}
	rest := strings.TrimLeft(line, "#")
	return strings.HasPrefix(rest, " ") && len(line)-len(rest) <= 6
}

func headingLevel(line string) int { return len(line) - len(strings.TrimLeft(line, "#")) }

// finishBlocks 补齐行号并对超长块二次切分（不合并小块：标题/函数语义需要保留）。
func finishBlocks(blocks []ChunkBlock) []ChunkBlock {
	for i := range blocks {
		if blocks[i].LineEnd == 0 {
			blocks[i].LineEnd = blocks[i].LineStart + strings.Count(blocks[i].Content, "\n")
		}
	}
	return splitOversize(blocks)
}

// ---- 代码：函数/类边界启发式切块（tree-sitter 接入前的降级实现） ----

// chunkCode 按顶层定义行（func/class/def/interface/type 等）切分；单块超长再切。
func chunkCode(content string) []ChunkBlock {
	lines := strings.Split(content, "\n")
	type sec struct {
		start int
		lines []string
	}
	var secs []sec
	var cur *sec
	flush := func() {
		if cur != nil && len(cur.lines) > 0 {
			secs = append(secs, *cur)
		}
		cur = nil
	}
	for i, line := range lines {
		if isTopDef(line) {
			flush()
			cur = &sec{start: i + 1}
		}
		if cur == nil {
			cur = &sec{start: i + 1}
		}
		cur.lines = append(cur.lines, line)
		if len(cur.lines) >= maxChunkRunes/2 { // 代码块按行数粗限
			flush()
		}
	}
	flush()
	if len(secs) <= 1 && len(lines) < 20 {
		return nil // 过短交给通用切块
	}

	var blocks []ChunkBlock
	for _, s := range secs {
		text := strings.Join(s.lines, "\n")
		if strings.TrimSpace(text) == "" {
			continue // 纯空白块（含仅换行）无语义
		}
		blocks = append(blocks, ChunkBlock{Content: text, LineStart: s.start, LineEnd: s.start + len(s.lines) - 1})
	}
	return finishBlocks(blocks)
}

// isTopDef 顶层定义行启发式（无缩进的常见语言定义关键词）。
func isTopDef(line string) bool {
	if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "}") {
		return false
	}
	for _, kw := range []string{"func ", "func(", "class ", "def ", "interface ", "type ", "struct ", "impl ", "public ", "private ", "export ", "const ", "service ", "fn "} {
		if strings.HasPrefix(line, kw) {
			return true
		}
	}
	return false
}

// ---- 通用文本：递归字符切块（重叠窗口） ----

func chunkText(content string) []ChunkBlock {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) == 0 {
		return nil
	}
	var blocks []ChunkBlock
	// 优先按段落聚合
	paras := splitParagraphs(content)
	var buf strings.Builder
	start := 1
	line := 1
	for _, p := range paras {
		// 行数按去尾换行后的实际行计（段尾换行是分隔符，不是新行）
		pLines := strings.Count(strings.TrimRight(p, "\n"), "\n") + 1
		if buf.Len() > 0 && utf8.RuneCountInString(buf.String())+utf8.RuneCountInString(p) > maxChunkRunes {
			blocks = append(blocks, ChunkBlock{Content: strings.TrimSpace(buf.String()), LineStart: start, LineEnd: line - 1})
			buf.Reset()
			start = line
		}
		if buf.Len() == 0 {
			start = line
		}
		buf.WriteString(p)
		buf.WriteString("\n")
		line += pLines
	}
	if strings.TrimSpace(buf.String()) != "" {
		blocks = append(blocks, ChunkBlock{Content: strings.TrimSpace(buf.String()), LineStart: start, LineEnd: line - 1})
	}
	return splitOversize(mergeSmall(blocks))
}

// splitParagraphs 按空行分段。
func splitParagraphs(content string) []string {
	var out []string
	var buf strings.Builder
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			if buf.Len() > 0 {
				out = append(out, buf.String())
				buf.Reset()
			}
			out = append(out, "\n")
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	if buf.Len() > 0 {
		out = append(out, buf.String())
	}
	return out
}

// ---- 公共精修：超长二次切分 ----

// splitOversize 超长块按 rune 窗口二次切分（带重叠）。
func splitOversize(blocks []ChunkBlock) []ChunkBlock {
	var out []ChunkBlock
	for _, b := range blocks {
		r := []rune(b.Content)
		if len(r) <= maxChunkRunes {
			out = append(out, b)
			continue
		}
		step := maxChunkRunes - overlapRunes
		for i := 0; i < len(r); i += step {
			end := i + maxChunkRunes
			if end > len(r) {
				end = len(r)
			}
			part := string(r[i:end])
			if strings.TrimSpace(part) == "" {
				break
			}
			nl := strings.Count(string(r[:i]), "\n")
			out = append(out, ChunkBlock{Content: part, HeadingPath: b.HeadingPath, LineStart: b.LineStart + nl, LineEnd: b.LineStart + nl + strings.Count(part, "\n")})
			if end == len(r) {
				break
			}
		}
	}
	return out
}

// mergeSmall 相邻同块小于 minChunkRunes 时并入前块。
func mergeSmall(blocks []ChunkBlock) []ChunkBlock {
	if len(blocks) == 0 {
		return nil
	}
	var out []ChunkBlock
	for _, b := range blocks {
		if len(out) > 0 && utf8.RuneCountInString(out[len(out)-1].Content) < minChunkRunes {
			prev := &out[len(out)-1]
			prev.Content += "\n" + b.Content
			prev.LineEnd = b.LineEnd
			if prev.HeadingPath == "" {
				prev.HeadingPath = b.HeadingPath
			}
			continue
		}
		out = append(out, b)
	}
	return out
}
