package rag

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- FetchLocalDir（上传文件落地目录的 reindex 拉取路径） ----

func TestFetchLocalDir(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	write("docs/readme.md", "# 指南")
	write("main.go", "package main")
	write("data.bin", "unsupported")      // R1 之外：静默跳过，不进清单
	write(".env", "SECRET=1")             // R1b 默认排除
	write("secrets/key.pem", "key")       // R1b 默认排除（目录规则）
	write("docs/drafts/wip.md", "草稿")     // 用户 ignore_rules 命中
	write("node_modules/dep.js", "var x") // skipDirs 跳过

	excl := ParseExclusionRules("")
	res, err := FetchLocalDir(context.Background(), dir, 1<<20, 100,
		[]string{"docs/drafts/**"}, excl)
	require.NoError(t, err)

	paths := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		paths = append(paths, f.Path)
	}
	assert.ElementsMatch(t, []string{"docs/readme.md", "main.go"}, paths, "仅受支持且未排除的文件带内容")
	// 目录级命中（secrets/、docs/drafts/**）走 SkipDir 整体跳过，不进清单；
	// 仅文件级命中才记录 Excluded。
	assert.Equal(t, []string{".env"}, res.Excluded)
	assert.Empty(t, res.Commit, "本地目录无 commit")
	assert.Positive(t, res.TotalSize)
	assert.Positive(t, res.FileSeen)
	assert.Empty(t, res.Oversized)
}

// 单文件超限 → Oversized 标记；仓库总量超限 → ErrRepoTooLarge。
func TestFetchLocalDirLimits(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.md")
	require.NoError(t, os.WriteFile(big, make([]byte, maxSingleFileBytes+1), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ok"), 0o600))

	excl := ParseExclusionRules("")
	res, err := FetchLocalDir(context.Background(), dir, 1<<30, 100, nil, excl)
	require.NoError(t, err)
	assert.Equal(t, []string{"big.md"}, res.Oversized, "超限文件进清单标记而非报错")
	require.Len(t, res.Files, 1)
	assert.Equal(t, "a.txt", res.Files[0].Path)

	// 仓库大小上限触发
	_, err = FetchLocalDir(context.Background(), dir, 4, 100, nil, excl)
	require.ErrorIs(t, err, ErrRepoTooLarge)
}

func TestFetchLocalDirTooManyFiles(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "f"+string(rune('a'+i))+".md"), []byte("x"), 0o600))
	}
	_, err := FetchLocalDir(context.Background(), dir, 1<<30, 2, nil, ParseExclusionRules(""))
	require.ErrorIs(t, err, ErrRepoTooLarge)
}

// ctx 取消 → 中止遍历。
func TestFetchLocalDirCtxCancel(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("x"), 0o600))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := FetchLocalDir(ctx, dir, 1<<20, 100, nil, ParseExclusionRules(""))
	require.ErrorIs(t, err, context.Canceled)
}

// ---- FetchURL（httptest 本地服务，无外部网络） ----

func TestFetchURL(t *testing.T) {
	page := `<!doctype html><html><head><title>部署指南</title></head><body>
		<nav>导航</nav><script>alert(1)</script>
		<article>正文第一步第二步</article>
		<footer>页脚</footer></body></html>`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer ts.Close()

	body, title, err := FetchURL(context.Background(), ts.URL)
	require.NoError(t, err)
	assert.Equal(t, "部署指南", title)
	text := string(body)
	assert.Contains(t, text, "正文第一步第二步")
	assert.NotContains(t, text, "alert")
	assert.NotContains(t, text, "导航", "nav 元素应被移除")
}

func TestFetchURLFailures(t *testing.T) {
	// 非法地址
	_, _, err := FetchURL(context.Background(), "ftp://example.com/x")
	require.ErrorIs(t, err, ErrURLUnreachable)

	// 非 HTML 内容类型
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
	}))
	defer ts.Close()
	_, _, err = FetchURL(context.Background(), ts.URL)
	require.ErrorIs(t, err, ErrURLNotHTML)

	// HTTP 404
	ts404 := httptest.NewServer(http.NotFoundHandler())
	defer ts404.Close()
	_, _, err = FetchURL(context.Background(), ts404.URL)
	require.ErrorIs(t, err, ErrURLUnreachable)
}

// ---- IsCodeExt / looksLikeCode ----

func TestIsCodeExt(t *testing.T) {
	for _, ext := range []string{".go", ".py", ".ts", ".rs", ".sql", ".sh", ".proto"} {
		assert.True(t, IsCodeExt(ext), ext)
	}
	for _, ext := range []string{".md", ".txt", ".yaml", ".json", ""} {
		assert.False(t, IsCodeExt(ext), ext)
	}
}
