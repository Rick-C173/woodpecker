package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"woodpecker/internal/model"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

// ---- 审计 P1-10：并发发起摄取只产生一条运行中任务 ----

// staleDocRepo 冻结文档状态写入：模拟「两个并发请求都读到 failed」的竞态窗口，
// 使两个请求都能通过 Retry 的失败态校验（走不到 422 分支）；
// 最终「同文档只允许一条运行中任务」由 CreateTask 的唯一约束兜底。
type staleDocRepo struct {
	*testutil.FakeDocRepo
}

func (s *staleDocRepo) SaveDocument(context.Context, *model.Document) error { return nil }

// TestDocEnqueueConcurrentOnlyOneTask 8 个并发 Retry：1 个成功，其余全部 DOC_TASK_RUNNING。
func TestDocEnqueueConcurrentOnlyOneTask(t *testing.T) {
	base := testutil.NewFakeDocs()
	queue := &testutil.FakeQueue{}
	env := testutil.NewTestEnv(t, nil)
	svc := service.NewDocumentService(&staleDocRepo{FakeDocRepo: base}, env.Settings,
		&testutil.FakeCipher{}, queue, env.AuditSvc)
	ctx := context.Background()

	failedDoc := base.SeedDoc(10, model.DocTypeGit, model.DocStatusFailed)

	const concurrency = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		okCount   int
		conflicts int
		otherErrs []error
	)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := svc.Retry(ctx, 1, 10, failedDoc.ID, "ip")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				okCount++
			case isTaskRunning(err):
				conflicts++
			default:
				otherErrs = append(otherErrs, err)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, okCount, "并发摄取只能有一个成功")
	assert.Equal(t, concurrency-1, conflicts, "其余全部得到 DOC_TASK_RUNNING")
	assert.Empty(t, otherErrs, "不应出现其它错误：%v", otherErrs)
	assert.Equal(t, 1, base.RunningTaskCount(failedDoc.ID), "库中只允许一条排队/运行中任务")
	assert.Len(t, queue.IDs, 1, "只入队一次")
}

// TestDocEnqueueFastPathStillRunning 快速失败路径保留：已存在运行中任务时直接 409，不建任务。
func TestDocEnqueueFastPathStillRunning(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()
	doc := e.docs.SeedDoc(10, model.DocTypeGit, model.DocStatusFailed)
	e.docs.SeedTask(doc.ID, model.TaskTypeFull) // 排队的运行中任务

	err := e.svc.Retry(ctx, 1, 10, doc.ID, "ip")
	assert.Equal(t, service.CodeDocTaskRunning, svcErr(t, err).Code)
	assert.Empty(t, e.queue.IDs, "409 时不再入队")
	assert.Equal(t, 1, e.docs.RunningTaskCount(doc.ID), "不得产生第二条任务")
}

// isTaskRunning 判定业务错误是否为 DOC_TASK_RUNNING（409 幂等冲突）。
func isTaskRunning(err error) bool {
	var se *service.ServiceError
	return errors.As(err, &se) && se.Code == service.CodeDocTaskRunning
}
