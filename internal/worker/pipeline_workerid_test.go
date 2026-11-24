package worker_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/worker"
)

// ---- P1-8：重启恢复只清理本 worker 的残留（多实例不得互杀）----

// TestPipelineRecoverStartupScopedToSelf A 启动恢复只清理 A 的残留，B 运行中的任务与文档原样保留。
func TestPipelineRecoverStartupScopedToSelf(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()
	e.pipeline.SetWorkerID("worker-a")

	docA := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	taskA := e.docs.SeedTaskWithWorker(docA.ID, model.TaskTypeFull, model.TaskStatusRunning, "worker-a")
	docB := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	taskB := e.docs.SeedTaskWithWorker(docB.ID, model.TaskTypeFull, model.TaskStatusRunning, "worker-b")

	e.pipeline.RecoverStartup(ctx, e.docs)

	assert.Equal(t, model.TaskStatusFailed, e.docs.TaskByID(taskA.ID).Status, "A 自己的残留置失败")
	assert.Equal(t, model.DocStatusFailed, e.docs.DocByID(docA.ID).Status)

	assert.Equal(t, model.TaskStatusRunning, e.docs.TaskByID(taskB.ID).Status, "B 的运行中任务不得被 A 清理")
	assert.Equal(t, model.DocStatusProcessing, e.docs.DocByID(docB.ID).Status, "B 的文档仍在处理中")
}

// TestPipelineRecoverStartupLegacyNullWorker 迁移前的历史数据（worker_id IS NULL）由首次启动清理。
func TestPipelineRecoverStartupLegacyNullWorker(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()
	e.pipeline.SetWorkerID("worker-a")

	legacyDoc := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	legacyTask := e.docs.SeedTaskWithWorker(legacyDoc.ID, model.TaskTypeFull, model.TaskStatusRunning, "")

	e.pipeline.RecoverStartup(ctx, e.docs)

	assert.Equal(t, model.TaskStatusFailed, e.docs.TaskByID(legacyTask.ID).Status, "无归属的历史任务应被清理")
	assert.Equal(t, model.DocStatusFailed, e.docs.DocByID(legacyDoc.ID).Status)
}

// TestPipelineProcessRecordsWorkerID 任务开始处理时写入本实例 worker_id（重启恢复的归属依据）。
func TestPipelineProcessRecordsWorkerID(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()
	e.pipeline.SetWorkerID("worker-a")

	d := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusPending)
	dir := filepath.Join(e.upload, "10", fmt.Sprint(d.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte("# 笔记\n\n正文"), 0o600))
	task := e.docs.SeedTask(d.ID, model.TaskTypeFull)
	require.NoError(t, e.pipeline.Process(ctx, task.ID))

	stored := e.docs.TaskByID(task.ID)
	require.NotNil(t, stored.WorkerID, "处理中应写入 worker_id")
	assert.Equal(t, "worker-a", *stored.WorkerID)
	assert.Equal(t, model.TaskStatusSuccess, stored.Status)
}

// TestPipelineWorkerIDDefault 未显式配置时回落主机名（非空即可，空值会误杀刚入队任务）。
func TestPipelineWorkerIDDefault(t *testing.T) {
	e := newPipeEnv(t)
	assert.NotEmpty(t, e.pipeline.WorkerID(), "缺省 worker_id 不得为空")

	e.pipeline.SetWorkerID("  ")
	assert.NotEmpty(t, e.pipeline.WorkerID(), "空值/空白应被忽略，保持缺省值")

	e.pipeline.SetWorkerID("worker-x")
	assert.Equal(t, "worker-x", e.pipeline.WorkerID())
}

// TestPipelineSetWorkerIDKeepsInterface worker.Pipeline 的 FailZombieStore 契约仍是
// 「按 worker 限定」的两方法签名（编译期断言，防止签名回退成全库清理）。
func TestPipelineSetWorkerIDKeepsInterface(_ *testing.T) {
	var _ worker.FailZombieStore = (*storeSpy)(nil)
}

// storeSpy 编译期校验 FailZombieStore 签名。
type storeSpy struct{}

func (s *storeSpy) FailZombieTasks(context.Context, string) (int64, []int64, error) {
	return 0, nil, nil
}

func (s *storeSpy) FailProcessingDocs(context.Context, []int64) (int64, error) { return 0, nil }
