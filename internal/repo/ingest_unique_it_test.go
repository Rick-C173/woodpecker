//go:build integration

// 「同文档运行中任务唯一」真库集成测试（审计 P1-10，迁移 000006 的部分唯一索引）。
package repo

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

// TestIngestTaskRunningUniqueIT P1-10：同一文档最多一条排队/运行中任务。
// 串行与并发两条路径都只有一个成功，另一个得到 ErrTaskRunning（→ DOC_TASK_RUNNING）。
func TestIngestTaskRunningUniqueIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "uniq.md", "race.md")

	// 串行：第二个排队任务被唯一索引拒绝，映射为 ErrTaskRunning
	first := &model.IngestTask{DocumentID: docs[0].ID, Type: model.TaskTypeFull, Status: model.TaskStatusQueued}
	require.NoError(t, r.CreateTask(ctx, first))
	dup := &model.IngestTask{DocumentID: docs[0].ID, Type: model.TaskTypeSync, Status: model.TaskStatusQueued}
	require.ErrorIs(t, r.CreateTask(ctx, dup), ErrTaskRunning, "唯一冲突应映射为 ErrTaskRunning")
	assert.Equal(t, int64(1), itCount(t, "SELECT COUNT(*) FROM ingest_tasks WHERE status IN (1,2)"))

	// 已结束任务不受限：同一文档可再建新任务
	first.Status = model.TaskStatusSuccess
	require.NoError(t, r.SaveTask(ctx, first))
	require.NoError(t, r.CreateTask(ctx, dup), "部分唯一索引只约束排队/运行中")

	// 并发：8 个 goroutine 抢建，只有一个成功
	const concurrency = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		okCount   int
		conflicts int
		others    []error
	)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := r.CreateTask(context.Background(), &model.IngestTask{
				DocumentID: docs[1].ID, Type: model.TaskTypeFull, Status: model.TaskStatusQueued,
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				okCount++
			case errors.Is(err, ErrTaskRunning):
				conflicts++
			default:
				others = append(others, err)
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, okCount, "并发只允许一个成功")
	assert.Equal(t, concurrency-1, conflicts, "其余均为唯一冲突")
	assert.Empty(t, others, "不应出现其它错误：%v", others)
	assert.Equal(t, int64(1),
		itCount(t, "SELECT COUNT(*) FROM ingest_tasks WHERE document_id = "+
			strconv.FormatInt(docs[1].ID, 10)+" AND status IN (1,2)"),
		"同一文档只允许一条排队/运行中任务")
}
