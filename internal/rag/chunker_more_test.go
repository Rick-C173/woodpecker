package rag

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- chunkMarkdown 分支 ----

// 代码围栏内的 # 不是标题；围栏开关双向切换。
func TestChunkMarkdownCodeFence(t *testing.T) {
	content := "# 文档\n\n```go\n# 这不是标题\nfunc main() {}\n```\n\n```text\n# 围栏2\n```\n\n# 第二篇\n\n正文"
	blocks := Chunk("markdown", content)
	require.Len(t, blocks, 2)
	assert.Equal(t, "文档", blocks[0].HeadingPath)
	assert.Contains(t, blocks[0].Content, "# 这不是标题", "围栏内容保留在正文里")
	assert.Equal(t, "第二篇", blocks[1].HeadingPath)
}

// 标题层级栈：路径拼接、层级回退、超深截断。
func TestChunkMarkdownHeadingStack(t *testing.T) {
	content := "# A\n## B\n### C\n内容一\n## D\n内容二"
	blocks := Chunk("markdown", content)
	require.Len(t, blocks, 2)
	assert.Equal(t, "A / B / C", blocks[0].HeadingPath)
	assert.Equal(t, "A / D", blocks[1].HeadingPath, "## 回退到第 2 层")

	// maxHeadingPath=6 截断：连续 7 层标题只保留最近 6 层路径
	deep := "# 1\n## 2\n### 3\n#### 4\n##### 5\n###### 6\n内容"
	blocks = Chunk("markdown", deep)
	require.Len(t, blocks, 1)
	assert.Equal(t, "1 / 2 / 3 / 4 / 5 / 6", blocks[0].HeadingPath)
}

// isHeading 边界：ATX 1~6 级且 # 后必须空格。
func TestIsHeading(t *testing.T) {
	assert.True(t, isHeading("# 标题"))
	assert.True(t, isHeading("###### 六级"))
	assert.False(t, isHeading("####### 七级"))
	assert.False(t, isHeading("#nospace"))
	assert.False(t, isHeading("正文 # 注释"))
	assert.False(t, isHeading("#"))
}

// 标题后无正文：不产出空块。
func TestChunkMarkdownEmptySection(t *testing.T) {
	blocks := Chunk("markdown", "# 只有标题\n\n# 另一个标题\n\n正文在这里")
	require.Len(t, blocks, 1)
	assert.Equal(t, "另一个标题", blocks[0].HeadingPath)
	assert.Contains(t, blocks[0].Content, "正文在这里")
}

// ---- chunkCode 分支 ----

// 过短代码（单定义 + 少于 20 行）退化为通用切块。
func TestChunkCodeShortFallsBack(t *testing.T) {
	code := "func main() {\n\tprintln(1)\n}\n"
	blocks := Chunk("code", code)
	require.Len(t, blocks, 1)
	assert.Empty(t, blocks[0].HeadingPath, "退化路径无标题语义")
}

// 代码块按行数粗限（400 行强制 flush）；splitOversize 重叠导致行号交错，
// 改断言首尾覆盖范围。
func TestChunkCodeLineLimitFlush(t *testing.T) {
	var b strings.Builder
	b.WriteString("func big() {\n")
	for i := 0; i < 900; i++ {
		b.WriteString("\tx++\n")
	}
	b.WriteString("}\n")
	blocks := Chunk("code", b.String())
	require.Greater(t, len(blocks), 1, "超过 400 行的段应被强制切分")
	assert.Equal(t, 1, blocks[0].LineStart)
	last := blocks[len(blocks)-1]
	assert.GreaterOrEqual(t, last.LineEnd, 902, "块组应覆盖全部源码行")
	// 每个块都在行数粗限以内（允许 splitOversize 重叠冗余）
	for _, blk := range blocks {
		assert.LessOrEqual(t, blk.LineEnd-blk.LineStart, maxChunkRunes/2+overlapRunes/10)
	}
}

// isTopDef：顶层定义识别与排除（缩进、右括号开头）。
func TestIsTopDef(t *testing.T) {
	for _, line := range []string{"func main() {", "func(x int) {", "class A:", "def f():", "type T struct {", "service Foo {", "fn main() {", "export default x"} {
		assert.True(t, isTopDef(line), line)
	}
	for _, line := range []string{"\tfunc 缩进不算", " func 缩进不算", "} 不算", "return x"} {
		assert.False(t, isTopDef(line), line)
	}
}

// 全空行内容：不产出块。
func TestChunkCodeBlankLines(t *testing.T) {
	code := strings.Repeat("\n", 40)
	assert.Empty(t, Chunk("code", code))
}

// ---- chunkText / splitOversize / mergeSmall ----

// 超长文本递归切分：窗口步进、重叠、行号偏移。
func TestChunkTextOversizeSplit(t *testing.T) {
	// 单段 > 800 rune：每行 ~10 rune，共 120 行 → 1200 rune
	var b strings.Builder
	for i := 0; i < 120; i++ {
		b.WriteString(strings.Repeat("字", 9) + "\n")
	}
	blocks := Chunk("text", b.String())
	require.Greater(t, len(blocks), 1)
	// 第一块恰好 800 rune 上限
	r1 := []rune(blocks[0].Content)
	assert.Len(t, r1, maxChunkRunes)
	// 重叠窗口：第二块起点 = 800-100
	if len(blocks) > 1 {
		r2 := []rune(blocks[1].Content)
		require.NotEmpty(t, r2)
		full := []rune(strings.TrimSpace(b.String()))
		end2 := len(r2)
		if end2 > maxChunkRunes {
			end2 = maxChunkRunes
		}
		assert.Equal(t, string(full[maxChunkRunes-overlapRunes:maxChunkRunes-overlapRunes+end2]), string(r2[:end2]),
			"第二块应从 step=700 处开始（带重叠）")
		// 行号偏移
		assert.Greater(t, blocks[1].LineStart, blocks[0].LineStart)
	}
}

// mergeSmall：小段并入前块；HeadingPath 继承。
func TestMergeSmall(t *testing.T) {
	// 三段各 50 rune：第一二段合并（<80 后并入），第三段独立
	seg := strings.Repeat("字", 50)
	content := seg + "\n\n" + seg + "\n\n" + seg
	blocks := chunkText(content)
	require.NotEmpty(t, blocks)
	merged := mergeSmall(blocks)
	for i := range merged {
		assert.GreaterOrEqual(t, len([]rune(merged[i].Content)), minChunkRunes,
			"mergeSmall 后不应再有小于 80 rune 的独立块（除末尾孤儿）")
	}
	// HeadingPath 继承：前块无标题时取并入块的
	a := ChunkBlock{Content: strings.Repeat("a", 79), HeadingPath: ""}
	bb := ChunkBlock{Content: strings.Repeat("b", 79), HeadingPath: "T"}
	out := mergeSmall([]ChunkBlock{a, bb})
	require.Len(t, out, 1)
	assert.Equal(t, "T", out[0].HeadingPath, "前块无标题应继承并入块标题")
}

// 空输入各路径：不产出块。
func TestChunkEmptyInputs(t *testing.T) {
	assert.Empty(t, Chunk("markdown", ""))
	assert.Empty(t, Chunk("code", ""))
	assert.Empty(t, Chunk("text", "   "))
}

// ---- 行号精确语义 ----

// markdown 块行号：LineStart=标题行，LineEnd=LineStart+正文行数。
func TestChunkMarkdownLineNumbers(t *testing.T) {
	content := "前言\n\n# 标题\n正文 L1\n正文 L2\n\n# 尾部\n尾文"
	blocks := Chunk("markdown", content)
	require.Len(t, blocks, 3)
	// 前言块（无标题）
	assert.Equal(t, 1, blocks[0].LineStart)
	assert.Equal(t, 1, blocks[0].LineEnd, "前言 1 行")
	// 标题块：标题在第 3 行；LineEnd 按"标题行 + 正文换行累计"语义 = 3 + 1 = 4
	assert.Equal(t, "标题", blocks[1].HeadingPath)
	assert.Equal(t, 3, blocks[1].LineStart)
	assert.Equal(t, 4, blocks[1].LineEnd)
	// 尾部：标题第 7 行，正文 1 行
	assert.Equal(t, 7, blocks[2].LineStart)
	assert.Equal(t, 7, blocks[2].LineEnd)
}

// splitParagraphs：按空行分段，空行占位保留行计数。
func TestSplitParagraphs(t *testing.T) {
	paras := splitParagraphs("第一段\n\n第二段 L1\n第二段 L2\n\n\n第三段")
	// 期望：["第一段\n", "\n", "第二段 L1\n第二段 L2\n", "\n", "\n", "第三段\n"]
	require.Len(t, paras, 6)
	assert.Equal(t, "第一段\n", paras[0])
	assert.Equal(t, "\n", paras[1])
	assert.Equal(t, "第二段 L1\n第二段 L2\n", paras[2])
	assert.Equal(t, "第三段\n", paras[5])
}

// chunkText 多段聚合的行号边界：块 LineStart/LineEnd 对应源文本行。
func TestChunkTextLineNumbers(t *testing.T) {
	// 三段各 40 rune，段间空行：聚合为一块 3 段 5 行
	seg := strings.Repeat("字", 40)
	content := seg + "\n\n" + seg + "\n\n" + seg
	blocks := chunkText(content)
	require.Len(t, blocks, 1, "120 rune < 800 应聚合为一块")
	assert.Equal(t, 1, blocks[0].LineStart)
	assert.Equal(t, 5, blocks[0].LineEnd, "3 个内容行 + 2 个空行")

	// 两段共 1600 rune：超出 800 切两块，块边界行号衔接
	big := strings.Repeat("字", 790) + "\n\n" + strings.Repeat("字", 790)
	blocks = chunkText(big)
	require.Len(t, blocks, 2)
	assert.Equal(t, 1, blocks[0].LineStart)
	assert.Equal(t, 3, blocks[1].LineStart, "第二块从第二段开始")
	assert.Equal(t, 3, blocks[1].LineEnd)
}
