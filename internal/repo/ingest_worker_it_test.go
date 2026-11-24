//go:build integration

// 摄取任务 worker 归属真库集成测试（审计 P1-8）。
package repo

import (
	"context"
	"testing"
	"time"

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
	n, docIDs, err := r.FailZombieTasks(ctx, wa, 5)
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

	// 运行中僵尸：10 分钟前开始且无心跳 → 时间兜底命中（实际崩溃场景的时间特征）
	stale := time.Now().Add(-10 * time.Minute)
	running := &model.IngestTask{DocumentID: docRunning.ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusRunning, StartedAt: &stale}
	queued := &model.IngestTask{DocumentID: docQueued.ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusQueued}
	require.NoError(t, r.CreateTask(ctx, running))
	require.NoError(t, r.CreateTask(ctx, queued))
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM ingest_tasks WHERE worker_id IS NULL"))

	n, docIDs, err := r.FailZombieTasks(ctx, "worker-any", 5)
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

// TestIngestTaskCrossWorkerStaleIT 验收回归 8-1（原本会永久卡死的场景）：
// 上一实例 pod-aaa 处理中崩溃，任务停在 running 且归属 pod-aaa；新实例 pod-bbb 启动时按归属
// 两段都不命中（旧实现的缺陷），必须靠心跳时间兜底回收，并把文档从处理中转为失败。
func TestIngestTaskCrossWorkerStaleIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "stuck.md")
	doc := docs[0]

	// 崩溃发生在 10 分钟前：最后一次心跳与开始时间都早于 5 分钟阈值
	stale := time.Now().Add(-10 * time.Minute)
	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusRunning, WorkerID: itStr("pod-aaa"),
		StartedAt: &stale, HeartbeatAt: &stale}
	require.NoError(t, r.CreateTask(ctx, task))

	// 新实例（pod-bbb）启动恢复
	n, docIDs, err := r.FailZombieTasks(ctx, "pod-bbb", 5)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "跨实例陈旧运行中任务必须被时间兜底回收")
	assert.Equal(t, []int64{doc.ID}, docIDs)

	docsFailed, err := r.FailProcessingDocs(ctx, docIDs)
	require.NoError(t, err)
	assert.Equal(t, int64(1), docsFailed)

	got, err := r.GetTaskByID(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusFailed, got.Status, "任务必须置失败（不再是永远 running）")
	require.NotNil(t, got.Error)
	assert.Contains(t, *got.Error, "服务重启")

	gotDoc, err := r.GetDocumentByID(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, model.DocStatusFailed, gotDoc.Status, "文档必须从处理中转为失败，用户才能重试")

	running, err := r.HasRunningTask(ctx, doc.ID)
	require.NoError(t, err)
	assert.False(t, running, "僵尸任务置失败后不得再有运行中任务挡住 retry/reindex")
}

// TestIngestTaskFreshHeartbeatKeptIT 有心跳的活任务（另一实例正在处理）不得被时间兜底误杀。
func TestIngestTaskFreshHeartbeatKeptIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "live.md")
	doc := docs[0]

	now := time.Now()
	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusRunning, WorkerID: itStr("pod-live"),
		StartedAt: &now, HeartbeatAt: &now}
	require.NoError(t, r.CreateTask(ctx, task))

	n, docIDs, err := r.FailZombieTasks(ctx, "pod-bbb", 5)
	require.NoError(t, err)
	assert.Zero(t, n, "有心跳的活任务不得被清理（否则多实例互杀，P1-8 回归）")
	assert.Empty(t, docIDs)

	got, err := r.GetTaskByID(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusRunning, got.Status)
	gotDoc, err := r.GetDocumentByID(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, model.DocStatusProcessing, gotDoc.Status)
}

// TestIngestTaskQueuedNeverReclaimedIT 红线回归：排队态（status=1）无论归属如何都不清理。
// 入队时 worker_id 为空、重排时保留旧归属；asynq 队列持久化会重投，DB 先置失败会造成库/队列不一致。
func TestIngestTaskQueuedNeverReclaimedIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "queued-null.md", "queued-self.md")

	old := time.Now().Add(-24 * time.Hour)
	unassigned := &model.IngestTask{DocumentID: docs[0].ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusQueued, CreatedAt: old}
	selfAssigned := &model.IngestTask{DocumentID: docs[1].ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusQueued, WorkerID: itStr("pod-bbb"), CreatedAt: old}
	require.NoError(t, r.CreateTask(ctx, unassigned))
	require.NoError(t, r.CreateTask(ctx, selfAssigned))

	n, docIDs, err := r.FailZombieTasks(ctx, "pod-bbb", 5)
	require.NoError(t, err)
	assert.Zero(t, n, "排队任务无论归属如何都不得被重启恢复清理")
	assert.Empty(t, docIDs)

	for _, id := range []int64{unassigned.ID, selfAssigned.ID} {
		got, gerr := r.GetTaskByID(ctx, id)
		require.NoError(t, gerr)
		assert.Equal(t, model.TaskStatusQueued, got.Status, "任务 %d 必须保持 queued", id)
		assert.Nil(t, got.Error, "不得写入重启中断的错误摘要")
		assert.Nil(t, got.FinishedAt)
	}
}

// TestHeartbeatTaskPersistsIT 心跳必须真正落库，且状态收尾的 SaveTask 不得把它覆盖回 NULL。
// （Process/fail/finishTask 用的都是从启动时读到的任务快照，其 heartbeat_at 恒为 nil。）
func TestHeartbeatTaskPersistsIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "hb.md")
	doc := docs[0]

	worker := "pod-aaa"
	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull,
		Status: model.TaskStatusRunning, WorkerID: &worker}
	require.NoError(t, r.CreateTask(ctx, task))
	require.Nil(t, task.HeartbeatAt, "新建任务尚无心跳")

	require.NoError(t, r.HeartbeatTask(ctx, task.ID))
	got, err := r.GetTaskByID(ctx, task.ID)
	require.NoError(t, err)
	require.NotNil(t, got.HeartbeatAt, "心跳必须落库")
	assert.WithinDuration(t, time.Now(), *got.HeartbeatAt, time.Minute)

	// 模拟 Process 收尾：拿「心跳之前读到的快照」再 SaveTask，不得把心跳清掉
	snapshot := *task
	snapshot.Status = model.TaskStatusFailed
	require.NoError(t, r.SaveTask(ctx, &snapshot))
	after, err := r.GetTaskByID(ctx, task.ID)
	require.NoError(t, err)
	assert.NotNil(t, after.HeartbeatAt, "SaveTask 不得把 heartbeat_at 覆盖回 NULL")
	assert.Equal(t, model.TaskStatusFailed, after.Status, "状态等其他字段仍要落库")
}
