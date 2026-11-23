package rag

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- R1 支持格式 ----

func TestIsSupportedExt(t *testing.T) {
	assert.True(t, IsSupportedExt("README.md"))
	assert.True(t, IsSupportedExt("a/b/main.GO"), "扩展名大小写不敏感")
	assert.True(t, IsSupportedExt("config.yaml"))
	assert.True(t, IsSupportedExt("Dockerfile"))
	assert.False(t, IsSupportedExt("image.png"))
	assert.False(t, IsSupportedExt("binary.exe"))
	assert.False(t, IsSupportedExt("noext"))
}

// ---- R1b 敏感排除 ----

func TestIsExcludedDefaults(t *testing.T) {
	excl := ParseExclusionRules("")
	cases := []struct {
		path string
		want bool
	}{
		{".env", true},
		{".env.local", true},
		{"config/.env.production", true},
		{"server.pem", true},
		{"certs/tls.key", true},
		{"id_rsa", true},
		{"credentials.json", true},
		{"my-credentials.json", false}, // credentials* 是前缀通配，my- 前缀不命中
		{"api_secret.go", true},
		{"cert.p12", true},
		{"secrets/db.yaml", true},
		{"a/b/secrets/c.yaml", true},
		{"README.md", false},
		{"src/main.go", false},
		{"internal/service/kb.go", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, IsExcluded(c.path, nil, excl), c.path)
	}
}

func TestIsExcludedUserRules(t *testing.T) {
	excl := ParseExclusionRules("")
	assert.True(t, IsExcluded("data/cache.tmp", []string{"*.tmp"}, excl))
	assert.True(t, IsExcluded("assets/logo.png", []string{"assets/"}, excl), "目录规则整目录命中")
	assert.False(t, IsExcluded("src/main.go", []string{"assets/"}, excl))
}

func TestParseExclusionRulesOverride(t *testing.T) {
	excl := ParseExclusionRules(`{"patterns":["*.bak"]}`)
	assert.True(t, IsExcluded("a.bak", nil, excl))
	assert.False(t, IsExcluded(".env", nil, excl), "覆盖后默认清单失效")
	excl2 := ParseExclusionRules("not-json")
	assert.True(t, IsExcluded(".env", nil, excl2), "解析失败回退默认")
}

// ---- 切块 ----

func TestChunkMarkdownHeadings(t *testing.T) {
	doc := "# 部署\n\n安装依赖。\n\n## 配置\n\n修改 config.yaml。\n\n### 环境变量\n\n设置 DATABASE_URL。\n\n## 运行\n\n执行 make dev。\n"
	blocks := Chunk("markdown", doc)
	require.GreaterOrEqual(t, len(blocks), 4)
	var foundCfg, foundEnv bool
	for _, b := range blocks {
		assert.NotEmpty(t, b.LineStart)
		if b.HeadingPath == "部署 / 配置" {
			foundCfg = true
			assert.Contains(t, b.Content, "config.yaml")
		}
		if b.HeadingPath == "部署 / 配置 / 环境变量" {
			foundEnv = true
			assert.Contains(t, b.Content, "DATABASE_URL")
		}
	}
	assert.True(t, foundCfg, "二级标题路径")
	assert.True(t, foundEnv, "三级标题路径")
}

func TestChunkMarkdownOversizeSection(t *testing.T) {
	long := strings.Repeat("很长的段落内容。", 400) // 约 2800 rune
	doc := "# 标题\n\n" + long + "\n"
	blocks := Chunk("markdown", doc)
	require.NotEmpty(t, blocks)
	for _, b := range blocks {
		assert.LessOrEqual(t, len([]rune(b.Content)), 800, "超长段落应二次切分")
	}
}

func TestChunkCodeBoundaries(t *testing.T) {
	code := "package main\n\nfunc A() {\n\t// a\n}\n\nfunc B() {\n\t// b\n}\n"
	blocks := Chunk("code", code)
	require.Len(t, blocks, 3)
	assert.Contains(t, blocks[0].Content, "package main")
	assert.Contains(t, blocks[1].Content, "func A()")
	assert.Contains(t, blocks[2].Content, "func B()")
	assert.Equal(t, 3, blocks[1].LineStart)
	assert.Equal(t, 7, blocks[2].LineStart)
}

func TestChunkTextParagraphMerge(t *testing.T) {
	text := strings.Repeat("短段落一。\n\n短段落二。\n\n", 3)
	blocks := Chunk("text", text)
	require.NotEmpty(t, blocks)
	assert.NotEmpty(t, blocks[0].Content)
}

func TestChunkEmpty(t *testing.T) {
	assert.Empty(t, Chunk("text", ""))
	assert.Empty(t, Chunk("text", "   \n  \n"))
}

// ---- 语言识别 ----

func TestDetectLanguage(t *testing.T) {
	assert.Equal(t, "Go", DetectLanguage("main.go"))
	assert.NotEmpty(t, DetectLanguage("app.py"))
}

// ---- ContentKind ----

func TestContentKind(t *testing.T) {
	assert.Equal(t, "markdown", ContentKind("a.md"))
	assert.Equal(t, "code", ContentKind("a.go"))
	assert.Equal(t, "code", ContentKind("Dockerfile"))
	assert.Equal(t, "text", ContentKind("a.yaml"))
	assert.Equal(t, "text", ContentKind("a.txt"))
}
