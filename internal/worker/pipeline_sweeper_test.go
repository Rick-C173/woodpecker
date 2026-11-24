package worker_test

// 周期性僵尸回收（sweeper）单测：覆盖「只按心跳判定、不碰活任务」与
// 「跟随 ctx 退出、不泄漏」两条关键性质（验收回归 8-1 补强）。

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/testutil"
	"woodpecker/internal/worker"
)

// countingStore 包装 FakeDocRepo，统计 FailStaleTasks 调用次数（验证 ticker 真的触发）。
type countingStore struct {
	*testutil.FakeDocRepo
	mu    sync.Mutex
	calls int
}

func (c *countingStore) FailStaleTasks(ctx context.Context, timeoutMinutes int) (int64, []int64, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.FakeDocRepo.FailStaleTasks(ctx, timeoutMinutes)
}

func (c *countingStore) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestSweepZombiesSparesLiveAndReclaimsStale 一次回收只动心跳超时的任务：
// 本实例正在跑的活任务（有心跳）不动，陈旧任务置失败并把文档转 failed。
func TestSweepZombiesSparesLiveAndReclaimsStale(t *testing.T) {
	store := &countingStore{FakeDocRepo: testutil.NewFakeDocs()}
	p := worker.NewPipeline(store, &testutil.FakeSettingRepo{}, &testutil.FakeCipher{}, &fakeResolver{}, &fakeEmbedder{}, &fakeQueue{})
	ctx := context.Background()

	liveDoc := store.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	live := store.SeedTaskWithWorker(liveDoc.ID, model.TaskTypeFull, model.TaskStatusRunning, "pod-self")
	require.NoError(t, store.HeartbeatTask(ctx, live.ID), "模拟活任务刚写过心跳")

	staleDoc := store.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	stale := store.SeedTaskWithWorker(staleDoc.ID, model.TaskTypeFull, model.TaskStatusRunning, "pod-aaa")
	store.BackdateTask(stale.ID, 10*time.Minute)

	n, err := p.SweepZombies(ctx)
	require.NoError(t, err)
	t.Logf("一次回收：任务数=%d；活任务(有心跳, 归属 pod-self) status=%d(running=2)；陈旧任务 status=%d(failed=4)",
		n, store.TaskByID(live.ID).Status, store.TaskByID(stale.ID).Status)
	assert.Equal(t, int64(1), n, "只回收心跳超时的那一个任务")

	assert.Equal(t, model.TaskStatusRunning, store.TaskByID(live.ID).Status, "有心跳的活任务不得被 sweeper 杀掉")
	assert.Equal(t, model.DocStatusProcessing, store.DocByID(liveDoc.ID).Status)

	assert.Equal(t, model.TaskStatusFailed, store.TaskByID(stale.ID).Status, "心跳超时的任务应置失败")
	assert.Equal(t, model.DocStatusFailed, store.DocByID(staleDoc.ID).Status, "对应文档应转失败")

	// 再次回收：没有新的僵尸 → 返回 0，且活任务依旧不受影响
	n, err = p.SweepZombies(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Equal(t, model.TaskStatusRunning, store.TaskByID(live.ID).Status)
}

// TestRunSweeperTicksAndExitsOnCancel ticker 到点确实执行回收；ctx 取消后 goroutine 退出且不再触发。
func TestRunSweeperTicksAndExitsOnCancel(t *testing.T) {
	store := &countingStore{FakeDocRepo: testutil.NewFakeDocs()}
	p := worker.NewPipeline(store, &testutil.FakeSettingRepo{}, &testutil.FakeCipher{}, &fakeResolver{}, &fakeEmbedder{}, &fakeQueue{})
	p.SetZombieSweepInterval(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.RunSweeper(ctx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && store.callCount() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	require.GreaterOrEqual(t, store.callCount(), 2, "ticker 到点必须触发回收（至少 2 次）")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 sweeper goroutine 必须退出（不得泄漏）")
	}

	stopped := store.callCount()
	time.Sleep(50 * time.Millisecond) // 5 个周期
	assert.Equal(t, stopped, store.callCount(), "退出后不得再触发回收")
}

// TestRunSweeperDisabledInterval sweeper 间隔 <=0 时直接返回（配置关闭时不得空转）。
func TestRunSweeperDisabledInterval(t *testing.T) {
	store := &countingStore{FakeDocRepo: testutil.NewFakeDocs()}
	p := worker.NewPipeline(store, &testutil.FakeSettingRepo{}, &testutil.FakeCipher{}, &fakeResolver{}, &fakeEmbedder{}, &fakeQueue{})
	p.SetZombieSweepInterval(0)

	done := make(chan struct{})
	go func() {
		defer close(done)
		p.RunSweeper(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("间隔 <=0 时应立即返回，不得阻塞或空转")
	}
	assert.Zero(t, store.callCount())
}
