package service_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

// ---- 审计 P1-9：上传「先落盘 → 后入库 → 再入队」，失败整条回滚 ----

// stagingPrefix 上传暂存目录前缀（{uploadDir}/{kbID}/.staging-XXXX，见 createOneUpload）。
const stagingPrefix = ".staging-"

// countFiles 递归统计目录下普通文件数（目录不存在视为 0）。
func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("遍历 %s 失败: %v", dir, err)
	}
	return n
}

// stagingDirs 列出目录下残留的暂存目录（应为空）。
func stagingDirs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, ent := range entries {
		if ent.IsDir() && strings.HasPrefix(ent.Name(), stagingPrefix) {
			out = append(out, ent.Name())
		}
	}
	return out
}

// TestDocUploadEnqueueFailureRollsBack 入队失败 → 库与磁盘都不留残留。
func TestDocUploadEnqueueFailureRollsBack(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()
	e.queue.Err = errors.New("redis down")

	_, _, err := e.svc.CreateUpload(ctx, 1, 10,
		[]service.UploadFile{{Filename: "a.md", Content: []byte("# A\n\n正文")}}, "ip")
	require.Error(t, err, "入队失败必须返回错误")
	assert.Contains(t, err.Error(), "任务入队")

	assert.Zero(t, e.docs.DocCount(), "入队失败不得留下 status=待处理 的僵尸文档")
	assert.Zero(t, e.docs.TaskCount(), "入队失败不得留下任务")
	assert.Zero(t, countFiles(t, e.upload), "入队失败不得留下已落盘文件")
	assert.Empty(t, stagingDirs(t, e.upload), "不得残留暂存目录")
	assert.Empty(t, e.queue.IDs, "未成功入队")
	assert.Zero(t, e.audit.CountAction("document.create"), "回滚后不记创建审计")
}

// TestDocUploadWriteFailureLeavesNoDocument 落盘失败 → 库中无 document。
func TestDocUploadWriteFailureLeavesNoDocument(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()

	// 用普通文件充当 uploadDir：其下无法建目录，稳定复现落盘失败（不依赖权限位，Windows 同样生效）
	blocked := filepath.Join(t.TempDir(), "blocked")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o600))
	e.svc.SetUploadDir(blocked)

	_, _, err := e.svc.CreateUpload(ctx, 1, 10,
		[]service.UploadFile{{Filename: "a.md", Content: []byte("# A")}}, "ip")
	require.Error(t, err, "落盘失败必须返回错误")
	assert.Zero(t, e.docs.DocCount(), "落盘失败不得入库任何文档")
	assert.Zero(t, e.docs.TaskCount(), "落盘失败不得建任务")
}

// TestDocUploadFileReadyBeforeEnqueue 文件必须在**入队前**位于 {uploadDir}/{kbID}/{docID}，
// 否则 worker（并发 4）取到任务时拉不到文件（P1-9 的核心不变量）。
func TestDocUploadFileReadyBeforeEnqueue(t *testing.T) {
	env := testutil.NewTestEnv(t, nil)
	docs := testutil.NewFakeDocs()
	probe := &probeQueue{}
	svc := service.NewDocumentService(docs, env.Settings, &testutil.FakeCipher{}, probe, env.AuditSvc)
	dir := t.TempDir()
	svc.SetUploadDir(dir)
	probe.uploadDir = dir

	items, rejected, err := svc.CreateUpload(context.Background(), 1, 10,
		[]service.UploadFile{{Filename: "README.md", Content: []byte("# 说明")}}, "ip")
	require.NoError(t, err)
	require.Empty(t, rejected)
	require.Len(t, items, 1)
	require.Len(t, probe.snapshots, 1)
	assert.True(t, probe.snapshots[0], "入队时刻文件应已在 {docID} 目录中就位")
	assert.Empty(t, stagingDirs(t, filepath.Join(dir, "10")), "成功后不应残留暂存目录")
}

// probeQueue 入队时刻探测「已落盘文件是否存在」的 Enqueuer 替身。
type probeQueue struct {
	uploadDir string
	snapshots []bool
}

func (q *probeQueue) EnqueueIngest(_ context.Context, _ int64) error {
	found := false
	entries, err := os.ReadDir(filepath.Join(q.uploadDir, "10"))
	if err == nil {
		for _, ent := range entries {
			// 暂存目录不算「已就位」：只认 {docID} 目录下的文件
			if !ent.IsDir() || strings.HasPrefix(ent.Name(), stagingPrefix) {
				continue
			}
			if countFilesNoT(filepath.Join(q.uploadDir, "10", ent.Name())) > 0 {
				found = true
			}
		}
	}
	q.snapshots = append(q.snapshots, found)
	return nil
}

// countFilesNoT 与 countFiles 同义，供无 testing.T 的场景使用。
// 遍历出错时返回 fs.SkipAll：WalkDir 视其为正常终止（不计错误）。
func countFilesNoT(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return fs.SkipAll
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}
