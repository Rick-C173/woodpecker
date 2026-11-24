package worker_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/testutil"
	"woodpecker/internal/worker"
)

// reclaimBeforeStageStore 模拟「旧 worker 准备推进阶段时，sweeper 已把任务与文档置 failed」。
type reclaimBeforeStageStore struct {
	*testutil.FakeDocRepo
	fired bool
}

func (s *reclaimBeforeStageStore) UpdateDocumentIfTaskRunning(ctx context.Context, taskID, documentID int64, updates map[string]any) (bool, error) {
	if !s.fired {
		s.fired = true
		if _, err := s.TransitionTaskIfRunning(ctx, taskID, map[string]any{"status": model.TaskStatusFailed}); err != nil {
			return false, err
		}
		if _, err := s.FailProcessingDocs(ctx, []int64{documentID}); err != nil {
			return false, err
		}
		return false, nil
	}
	return s.FakeDocRepo.UpdateDocumentIfTaskRunning(ctx, taskID, documentID, updates)
}

// TestProcessAbandonsWhenReclaimedBeforeStage 审查 #5 回归：
// 僵尸 worker 不得把 sweeper 已置 failed 的文档覆盖回 processing，否则文档永久卡死
// （Retry 只接受 failed、Sync 拒绝 processing，用户无恢复入口）。
func TestProcessAbandonsWhenReclaimedBeforeStage(t *testing.T) {
	base := testutil.NewFakeDocs()
	store := &reclaimBeforeStageStore{FakeDocRepo: base}
	p := worker.NewPipeline(store, &testutil.FakeSettingRepo{}, &testutil.FakeCipher{},
		&fakeResolver{}, &fakeEmbedder{}, &fakeQueue{})

	d := base.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	task := base.SeedTask(d.ID, model.TaskTypeFull)

	require.NoError(t, p.Process(context.Background(), task.ID))

	assert.Equal(t, model.DocStatusFailed, base.DocByID(d.ID).Status,
		"被回收后文档必须保持 failed，不得被僵尸改回 processing")
	assert.Equal(t, model.TaskStatusFailed, base.TaskByID(task.ID).Status)
}

// reclaimBeforeFinishStore 模拟「分块已写入、收尾前任务被 sweeper 回收」。
type reclaimBeforeFinishStore struct {
	*testutil.FakeDocRepo
}

func (s *reclaimBeforeFinishStore) FinishTaskIfRunning(ctx context.Context, taskID, documentID int64, _, _ map[string]any) (bool, error) {
	if _, err := s.TransitionTaskIfRunning(ctx, taskID, map[string]any{"status": model.TaskStatusFailed}); err != nil {
		return false, err
	}
	if _, err := s.FailProcessingDocs(ctx, []int64{documentID}); err != nil {
		return false, err
	}
	return false, nil
}

// TestProcessFencedAtFinishKeepsFailedDoc 审查 #5 回归：
// 收尾被 fencing 拒绝时，文档必须保持回收方写入的 failed，不得先落 ready 再失败。
func TestProcessFencedAtFinishKeepsFailedDoc(t *testing.T) {
	base := testutil.NewFakeDocs()
	store := &reclaimBeforeFinishStore{FakeDocRepo: base}
	p := worker.NewPipeline(store, &testutil.FakeSettingRepo{}, &testutil.FakeCipher{},
		&fakeResolver{}, &fakeEmbedder{}, &fakeQueue{})
	dir := t.TempDir()
	p.SetUploadDir(dir)

	d := base.SeedDoc(10, model.DocTypeUpload, model.DocStatusPending)
	docDir := filepath.Join(dir, "10", "1")
	require.NoError(t, os.MkdirAll(docDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(docDir, "README.md"), []byte("# 指南\n\n内容"), 0o600))
	task := base.SeedTask(d.ID, model.TaskTypeFull)

	require.NoError(t, p.Process(context.Background(), task.ID))

	doc := base.DocByID(d.ID)
	assert.Equal(t, model.DocStatusFailed, doc.Status, "收尾被拒时不得把文档写成 ready")
	assert.Nil(t, doc.Stage)
	assert.Equal(t, model.TaskStatusFailed, base.TaskByID(task.ID).Status)
}
