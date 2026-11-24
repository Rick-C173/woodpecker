//go:build integration

// 摄取任务 worker 归属真库集成测试（审计 P1-8）。
package repo

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

// TestIngestTaskWorkerScopeIT P1-8：重启恢复只清理本 worker 的残留，不得杀掉其它实例的任务。
func TestIngestTaskWorkerScopeIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "a.md", "b.md")
	docA, docB := docs[0], docs[1]

	wa, wb := "worker-a", "worker-b"
	taskA := &model.IngestTask{DocumentID: docA.ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusRunning, WorkerID: &wa}
	taskB := &model.IngestTask{DocumentID: docB.ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusRunning, WorkerID: &wb}
	require.NoError(t, r.CreateTask(ctx, taskA))
	require.NoError(t, r.CreateTask(ctx, taskB))

	// A 启动恢复：只清理自己的
	n, docIDs, err := r.FailZombieTasks(ctx, wa)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "只清理 A 自己的运行中任务")
	assert.Equal(t, []int64{docA.ID}, docIDs)

	docsFailed, err := r.FailProcessingDocs(ctx, docIDs)
	require.NoError(t, err)
	assert.Equal(t, int64(1), docsFailed, "只影响 A 的文档")

	gotA, err := r.GetTaskByID(ctx, taskA.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailed, gotA.Status)
	require.NotNil(t, gotA.Error)
	assert.Contains(t, *gotA.Error, "服务重启")

	gotB, err := r.GetTaskByID(ctx, taskB.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusRunning, gotB.Status, "B 的运行中任务不得被 A 清理")
	require.NotNil(t, gotB.WorkerID)
	assert.Equal(t, wb, *gotB.WorkerID, "worker_id 已落库")
	gotDocB, err := r.GetDocumentByID(ctx, docB.ID)
	require.NoError(t, err)
	assert.Equal(t, model.DocStatusProcessing, gotDocB.Status, "B 的文档仍在处理中")
}

// TestIngestTaskLegacyNullWorkerIT P1-8：无归属（worker_id IS NULL）时只清理**运行中**的僵尸；
// 排队中（status=1）的任务不动——asynq 队列持久化，重启后会重投，DB 先置失败会造成库/队列不一致。
func TestIngestTaskLegacyNullWorkerIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "legacy-running.md", "legacy-queued.md")
	docRunning, docQueued := docs[0], docs[1]

	// 排队中文档：服务层入队时文档是 pending（对齐 CreateUpload 的真实状态）
	docQueued.Status = model.DocStatusPending
	require.NoError(t, r.SaveDocument(ctx, docQueued))

	running := &model.IngestTask{DocumentID: docRunning.ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusRunning}
	queued := &model.IngestTask{DocumentID: docQueued.ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusQueued}
	require.NoError(t, r.CreateTask(ctx, running))
	require.NoError(t, r.CreateTask(ctx, queued))
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM ingest_tasks WHERE worker_id IS NULL"))

	n, docIDs, err := r.FailZombieTasks(ctx, "worker-any")
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "只清理无归属的运行中僵尸")
	assert.Equal(t, []int64{docRunning.ID}, docIDs)

	docsFailed, err := r.FailProcessingDocs(ctx, docIDs)
	require.NoError(t, err)
	assert.Equal(t, int64(1), docsFailed)

	gotQueued, err := r.GetTaskByID(ctx, queued.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusQueued, gotQueued.Status, "排队中且无归属的任务必须保持 queued")
	assert.Nil(t, gotQueued.Error, "不得写入重启中断的错误摘要")
	assert.Nil(t, gotQueued.FinishedAt)
	gotDocQueued, err := r.GetDocumentByID(ctx, docQueued.ID)
	require.NoError(t, err)
	assert.Equal(t, model.DocStatusPending, gotDocQueued.Status, "排队中文档不得被置失败")

	gotRunning, err := r.GetTaskByID(ctx, running.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailed, gotRunning.Status, "运行中僵尸应置失败")
}
