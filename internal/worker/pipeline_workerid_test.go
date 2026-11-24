package worker_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestPipelineRecoverStartupLegacyNullWorker 升级前无归属（worker_id IS NULL）的**运行中**任务
// 由启动恢复清理（心跳缺失时按 started_at 判定，故需构造「确实已死」的陈旧任务）。
func TestPipelineRecoverStartupLegacyNullWorker(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()
	e.pipeline.SetWorkerID("worker-a")

	legacyDoc := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	legacyTask := e.docs.SeedTaskWithWorker(legacyDoc.ID, model.TaskTypeFull, model.TaskStatusRunning, "")
	e.docs.BackdateTask(legacyTask.ID, time.Hour) // 10 分钟以上无心跳 → 时间兜底命中

	e.pipeline.RecoverStartup(ctx, e.docs)

	assert.Equal(t, model.TaskStatusFailed, e.docs.TaskByID(legacyTask.ID).Status, "无归属的运行中僵尸应被清理")
	assert.Equal(t, model.DocStatusFailed, e.docs.DocByID(legacyDoc.ID).Status)
}

// TestPipelineRecoverStartupKeepsQueuedUnassigned 无归属的**排队中**任务不得被重启恢复清理。
//
// 入队时 worker_id 为空，只有 Process 开始处理才写归属；asynq 队列持久化在 Redis，
// 重启后会重投这些排队任务。若在 DB 里先置失败，就会出现「用户看到失败、任务其实还在跑」
// 的库/队列不一致。运行中的无归属任务仍按僵尸清理（见上一条用例）。
func TestPipelineRecoverStartupKeepsQueuedUnassigned(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()
	e.pipeline.SetWorkerID("worker-a")

	queuedDoc := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusPending)
	queued := e.docs.SeedTaskWithWorker(queuedDoc.ID, model.TaskTypeFull, model.TaskStatusQueued, "")

	zombieDoc := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	zombie := e.docs.SeedTaskWithWorker(zombieDoc.ID, model.TaskTypeFull, model.TaskStatusRunning, "")
	e.docs.BackdateTask(zombie.ID, time.Hour) // 陈旧运行中任务 → 时间兜底命中

	e.pipeline.RecoverStartup(ctx, e.docs)

	assert.Equal(t, model.TaskStatusQueued, e.docs.TaskByID(queued.ID).Status,
		"排队中且无归属的任务必须保持 queued，交给 asynq 重投")
	assert.Nil(t, e.docs.TaskByID(queued.ID).Error, "不得写入重启中断的错误摘要")
	assert.Equal(t, model.DocStatusPending, e.docs.DocByID(queuedDoc.ID).Status,
		"对应文档不得被置失败")

	assert.Equal(t, model.TaskStatusFailed, e.docs.TaskByID(zombie.ID).Status,
		"运行中且无归属的僵尸仍要清理")
	assert.Equal(t, model.DocStatusFailed, e.docs.DocByID(zombieDoc.ID).Status)
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

// TestPipelineRecoverStartupReclaimsCrossInstanceStale 验收回归 8-1：
// 上一实例 pod-aaa 崩溃留下的 running 任务（心跳 10 分钟未更新），新实例 pod-bbb 启动时必须被回收。
// 旧实现只按 worker_id 匹配（pod-bbb ≠ pod-aaa）→ 两段条件都不命中 → 任务永久 running、文档永久处理中。
func TestPipelineRecoverStartupReclaimsCrossInstanceStale(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()
	e.pipeline.SetWorkerID("pod-bbb")

	doc := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	task := e.docs.SeedTaskWithWorker(doc.ID, model.TaskTypeFull, model.TaskStatusRunning, "pod-aaa")
	e.docs.BackdateTask(task.ID, 10*time.Minute) // 心跳/开始时间均早于 5 分钟阈值

	e.pipeline.RecoverStartup(ctx, e.docs)

	assert.Equal(t, model.TaskStatusFailed, e.docs.TaskByID(task.ID).Status,
		"归属陈旧但已无心跳的运行中任务必须被时间兜底回收")
	assert.Equal(t, model.DocStatusFailed, e.docs.DocByID(doc.ID).Status,
		"对应文档必须从处理中转为失败，用户才能重试")
}

// TestPipelineRecoverStartupKeepsLiveHeartbeat 有心跳的活任务（归属其它实例）不得被时间兜底误杀。
func TestPipelineRecoverStartupKeepsLiveHeartbeat(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()
	e.pipeline.SetWorkerID("pod-bbb")

	doc := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	task := e.docs.SeedTaskWithWorker(doc.ID, model.TaskTypeFull, model.TaskStatusRunning, "pod-live")
	require.NoError(t, e.docs.HeartbeatTask(ctx, task.ID), "模拟活任务刚写过心跳")

	e.pipeline.RecoverStartup(ctx, e.docs)

	assert.Equal(t, model.TaskStatusRunning, e.docs.TaskByID(task.ID).Status,
		"有心跳的活任务不得被清理（否则多实例互杀）")
	assert.Equal(t, model.DocStatusProcessing, e.docs.DocByID(doc.ID).Status)
}

// storeSpy 编译期校验 FailZombieStore 签名（含心跳超时阈值参数，验收回归 8-1）。
type storeSpy struct{}

func (s *storeSpy) FailZombieTasks(context.Context, string, int) (int64, []int64, error) {
	return 0, nil, nil
}

func (s *storeSpy) FailProcessingDocs(context.Context, []int64) (int64, error) { return 0, nil }
