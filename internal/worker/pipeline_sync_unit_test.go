package worker_test

// 验收回归 8-2 单测版（不依赖 PostgreSQL，走 FakeDocRepo 真实内存分块实现）。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

// TestPipelineSyncClearsChunksOfEmptiedFile 验收回归 8-2（单测版，不依赖 PG）：
// 文件被清空（切不出块）后，增量同步必须删掉该文件的旧分块，未变更文件不受影响。
func TestPipelineSyncClearsChunksOfEmptiedFile(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()

	d := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusPending)
	dir := filepath.Join(e.upload, "10", fmt.Sprint(d.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	aPath := filepath.Join(dir, "a.md")
	require.NoError(t, os.WriteFile(aPath, []byte("# A\n\n内容甲。\n\n## A2\n\n甲二。\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("# B\n\n内容乙。\n"), 0o600))

	full := e.docs.SeedTask(d.ID, model.TaskTypeFull)
	require.NoError(t, e.pipeline.Process(ctx, full.ID))

	countByFile := func() map[string]int {
		out := map[string]int{}
		for _, c := range e.docs.GetChunks(d.ID) {
			if c.FilePath != nil {
				out[*c.FilePath]++
			}
		}
		return out
	}
	before := countByFile()
	require.Positive(t, before["a.md"], "全量后 a.md 应有分块")
	require.Positive(t, before["b.md"], "全量后 b.md 应有分块")

	// 清空 a.md → 该文件本轮切不出块（groups 中仍保留空组）
	require.NoError(t, os.WriteFile(aPath, []byte(""), 0o600))
	sync := e.docs.SeedTask(d.ID, model.TaskTypeSync)
	require.NoError(t, e.pipeline.Process(ctx, sync.ID))

	after := countByFile()
	assert.Zero(t, after["a.md"], "a.md 已清空 → 旧分块必须被删除（旧内容不得残留可检索）")
	assert.Equal(t, before["b.md"], after["b.md"], "b.md 未变更 → 分块数必须不变")
	assert.Equal(t, model.DocStatusReady, e.docs.DocByID(d.ID).Status)
}
