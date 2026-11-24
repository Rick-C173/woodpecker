//go:build integration

// 分块写入 fencing 真库集成测试（审查 #1 补强）。
package repo

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

// TestReplaceFileChunksFencedByTaskStatusIT 任务被回收后，文件元数据与分块必须**一起**拒绝写入。
//
// 为什么必须有这条：只守「任务状态收尾」不够——若 content_hash 先落库而 chunks 没写，
// 新任务会因 hash 相同跳过该文件，文档永久缺块。本用例断言被 fencing 时两者都未落库。
func TestReplaceFileChunksFencedByTaskStatusIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, kb, docs := itSeedDocKB(t, "a.md")
	doc := docs[0]

	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeSync, Status: model.TaskStatusRunning}
	require.NoError(t, r.CreateTask(ctx, task))

	// 1) 运行中：元数据 + 分块同一事务写入
	h1 := "hash-v1"
	f1 := &model.DocumentFile{DocumentID: doc.ID, Path: "a.md", Status: model.FileStatusIndexed, ContentHash: &h1}
	c1 := []model.Chunk{
		{DocumentID: doc.ID, KBID: kb.ID, Content: "旧块一", ChunkIndex: 0, FilePath: itStr("a.md"), Embedding: itVec(0.1)},
		{DocumentID: doc.ID, KBID: kb.ID, Content: "旧块二", ChunkIndex: 1, FilePath: itStr("a.md"), Embedding: itVec(0.2)},
	}
	applied, err := r.ReplaceFileChunksIfTaskRunning(ctx, task.ID, doc.ID, "a.md", f1, c1)
	require.NoError(t, err)
	require.True(t, applied, "运行中的任务必须能写入")

	docID := strconv.FormatInt(doc.ID, 10)
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID+" AND file_path = 'a.md'"))
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID+" AND file_id IS NOT NULL"),
		"file_id 应由写入事务 upsert 后回填")

	// 2) 模拟 sweeper 回收：任务置 failed（与 FailStaleTasks 的终态一致）
	ok, err := r.TransitionTaskIfRunning(ctx, task.ID, map[string]any{"status": model.TaskStatusFailed})
	require.NoError(t, err)
	require.True(t, ok)

	// 3) 原 worker 迟到写入：必须被 fencing 拒绝，且不留下任何变更
	h2 := "hash-v2"
	f2 := &model.DocumentFile{DocumentID: doc.ID, Path: "a.md", Status: model.FileStatusIndexed, ContentHash: &h2}
	c2 := []model.Chunk{
		{DocumentID: doc.ID, KBID: kb.ID, Content: "新块", ChunkIndex: 0, FilePath: itStr("a.md"), Embedding: itVec(0.9)},
	}
	applied, err = r.ReplaceFileChunksIfTaskRunning(ctx, task.ID, doc.ID, "a.md", f2, c2)
	require.NoError(t, err, "fencing 不算错误：调用方据此放弃而非重试")
	assert.False(t, applied, "任务已被回收，写入必须被拒绝")

	files, err := r.ListFiles(ctx, doc.ID)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.NotNil(t, files[0].ContentHash)
	assert.Equal(t, "hash-v1", *files[0].ContentHash, "被 fencing 时 content_hash 不得被覆盖")
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID),
		"分块数不得变化")
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID+" AND content = '新块'"),
		"新块不得写入")

	// 4) 任务行不存在（文档级联删除）同样按 fencing 处理，不报错
	applied, err = r.ReplaceFileChunksIfTaskRunning(ctx, 999999, doc.ID, "a.md", f2, c2)
	require.NoError(t, err)
	assert.False(t, applied, "任务行不存在时不得写入")
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID))
}

// TestReplaceDocumentChunksFencedByTaskStatusIT 全量路径（整篇替换）同样受 fencing 保护。
func TestReplaceDocumentChunksFencedByTaskStatusIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, kb, docs := itSeedDocKB(t, "a.md", "b.md")
	doc := docs[0]

	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull, Status: model.TaskStatusRunning}
	require.NoError(t, r.CreateTask(ctx, task))

	h1 := "hash-a1"
	files := []*model.DocumentFile{
		{DocumentID: doc.ID, Path: "a.md", Status: model.FileStatusIndexed, ContentHash: &h1},
	}
	chunks := []model.Chunk{
		{DocumentID: doc.ID, KBID: kb.ID, Content: "块一", ChunkIndex: 0, FilePath: itStr("a.md"), Embedding: itVec(0.3)},
		{DocumentID: doc.ID, KBID: kb.ID, Content: "块二", ChunkIndex: 1, FilePath: itStr("a.md"), Embedding: itVec(0.4)},
	}
	applied, err := r.ReplaceDocumentChunksIfTaskRunning(ctx, task.ID, doc.ID, files, chunks)
	require.NoError(t, err)
	require.True(t, applied)

	docID := strconv.FormatInt(doc.ID, 10)
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID))
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID+" AND file_id IS NOT NULL"))

	ok, err := r.TransitionTaskIfRunning(ctx, task.ID, map[string]any{"status": model.TaskStatusFailed})
	require.NoError(t, err)
	require.True(t, ok)

	h2 := "hash-a2"
	files2 := []*model.DocumentFile{
		{DocumentID: doc.ID, Path: "a.md", Status: model.FileStatusIndexed, ContentHash: &h2},
	}
	chunks2 := []model.Chunk{
		{DocumentID: doc.ID, KBID: kb.ID, Content: "被拒的新块", ChunkIndex: 0, FilePath: itStr("a.md"), Embedding: itVec(0.8)},
	}
	applied, err = r.ReplaceDocumentChunksIfTaskRunning(ctx, task.ID, doc.ID, files2, chunks2)
	require.NoError(t, err)
	assert.False(t, applied, "整篇替换同样必须被 fencing 拒绝")

	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID), "分块不得被清空或替换")
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID+" AND content = '被拒的新块'"))
	got, err := r.ListFiles(ctx, doc.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].ContentHash)
	assert.Equal(t, "hash-a1", *got[0].ContentHash, "整篇替换被拒时 hash 不得更新")
}

// TestUpdateDocumentFencedByTaskStatusIT 任务被回收后，僵尸 worker 不得把文档改回 processing
// （审查 #5：否则文档永久卡死，Retry 只接受 failed、Sync 拒绝 processing）。
func TestUpdateDocumentFencedByTaskStatusIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "a.md")
	doc := docs[0]

	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull, Status: model.TaskStatusRunning}
	require.NoError(t, r.CreateTask(ctx, task))

	applied, err := r.UpdateDocumentIfTaskRunning(ctx, task.ID, doc.ID, map[string]any{
		"status": model.DocStatusProcessing, "stage": model.DocStageEmbed, "updated_at": time.Now(),
	})
	require.NoError(t, err)
	require.True(t, applied, "运行中的任务必须能推进阶段")

	// 模拟 sweeper：任务置 failed、文档置 failed
	ok, err := r.TransitionTaskIfRunning(ctx, task.ID, map[string]any{"status": model.TaskStatusFailed})
	require.NoError(t, err)
	require.True(t, ok)
	n, err := r.FailProcessingDocs(ctx, []int64{doc.ID})
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	// 旧 worker 迟到推进阶段：必须被拒，文档保持 failed
	applied, err = r.UpdateDocumentIfTaskRunning(ctx, task.ID, doc.ID, map[string]any{
		"status": model.DocStatusProcessing, "stage": model.DocStageChunk, "updated_at": time.Now(),
	})
	require.NoError(t, err)
	assert.False(t, applied, "任务已回收，阶段写入必须被拒绝")

	got, err := r.GetDocumentByID(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, model.DocStatusFailed, got.Status, "文档不得被僵尸改回 processing")
	assert.Nil(t, got.Stage)
}

// TestMaintainFilesFencedByTaskStatusIT 清单维护（删除远端已删文件、写排除/超大标记）同样受 fencing 保护。
func TestMaintainFilesFencedByTaskStatusIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, kb, docs := itSeedDocKB(t, "a.md")
	doc := docs[0]

	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeSync, Status: model.TaskStatusRunning}
	require.NoError(t, r.CreateTask(ctx, task))

	h := "hash-a"
	applied, err := r.ReplaceFileChunksIfTaskRunning(ctx, task.ID, doc.ID, "a.md",
		&model.DocumentFile{DocumentID: doc.ID, Path: "a.md", Status: model.FileStatusIndexed, ContentHash: &h},
		[]model.Chunk{{DocumentID: doc.ID, KBID: kb.ID, Content: "旧块", ChunkIndex: 0, FilePath: itStr("a.md"), Embedding: itVec(0.1)}})
	require.NoError(t, err)
	require.True(t, applied)

	// sweeper 回收任务
	ok, err := r.TransitionTaskIfRunning(ctx, task.ID, map[string]any{"status": model.TaskStatusFailed})
	require.NoError(t, err)
	require.True(t, ok)

	// 僵尸尝试删除 a.md 并写入 b.md 的排除标记：必须整笔拒绝
	applied, err = r.MaintainFilesIfTaskRunning(ctx, task.ID, doc.ID, []string{"a.md"},
		[]*model.DocumentFile{{DocumentID: doc.ID, Path: "b.md", Status: model.FileStatusExcluded}})
	require.NoError(t, err)
	assert.False(t, applied, "任务已回收，清单维护必须被拒绝")

	docID := strconv.FormatInt(doc.ID, 10)
	files, err := r.ListFiles(ctx, doc.ID)
	require.NoError(t, err)
	require.Len(t, files, 1, "僵尸不得删除文件记录或写入新标记")
	assert.Equal(t, "a.md", files[0].Path)
	assert.Equal(t, int64(1), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID), "分块不得被删")

	// 新任务接管后允许同样的维护
	task2 := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeSync, Status: model.TaskStatusRunning}
	require.NoError(t, r.CreateTask(ctx, task2))
	applied, err = r.MaintainFilesIfTaskRunning(ctx, task2.ID, doc.ID, []string{"a.md"},
		[]*model.DocumentFile{{DocumentID: doc.ID, Path: "b.md", Status: model.FileStatusExcluded}})
	require.NoError(t, err)
	require.True(t, applied)

	files, err = r.ListFiles(ctx, doc.ID)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "b.md", files[0].Path)
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+docID))
}

// TestFinishTaskFencedByTaskStatusIT 任务终态与文档终态必须同事务生效；被回收后两者都不写。
func TestFinishTaskFencedByTaskStatusIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "a.md")
	doc := docs[0]

	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull, Status: model.TaskStatusRunning}
	require.NoError(t, r.CreateTask(ctx, task))

	applied, err := r.FinishTaskIfRunning(ctx, task.ID, doc.ID,
		map[string]any{"status": model.TaskStatusSuccess, "finished_at": time.Now()},
		map[string]any{"status": model.DocStatusReady, "stage": nil, "error": nil})
	require.NoError(t, err)
	require.True(t, applied)

	got, err := r.GetDocumentByID(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, model.DocStatusReady, got.Status)
	assert.Nil(t, got.Stage)
	finished, err := r.GetTaskByID(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, finished.Status)

	// 已完成的任务再被「收尾」：整笔拒绝，不得把 ready 覆盖回 failed
	applied, err = r.FinishTaskIfRunning(ctx, task.ID, doc.ID,
		map[string]any{"status": model.TaskStatusFailed, "finished_at": time.Now()},
		map[string]any{"status": model.DocStatusFailed, "stage": nil})
	require.NoError(t, err)
	assert.False(t, applied)

	got, err = r.GetDocumentByID(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, model.DocStatusReady, got.Status, "文档终态不得被覆盖")
	finished, err = r.GetTaskByID(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, finished.Status, "任务终态不得被覆盖")
}

// TestIsTaskRunningIT 快速失败检查与真实状态一致。
func TestIsTaskRunningIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, _, docs := itSeedDocKB(t, "a.md")
	doc := docs[0]

	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeSync, Status: model.TaskStatusQueued}
	require.NoError(t, r.CreateTask(ctx, task))

	running, err := r.IsTaskRunning(ctx, task.ID)
	require.NoError(t, err)
	assert.False(t, running, "queued 不算运行中")

	ok, err := r.TransitionTaskIfRunning(ctx, task.ID, map[string]any{"status": model.TaskStatusRunning})
	require.NoError(t, err)
	assert.False(t, ok, "queued → running 不走 TransitionTaskIfRunning（它只接受 running 前态）")

	// 直接置 running（模拟 Process 开工）
	require.NoError(t, itDB.Exec("UPDATE ingest_tasks SET status = ? WHERE id = ?", model.TaskStatusRunning, task.ID).Error)
	running, err = r.IsTaskRunning(ctx, task.ID)
	require.NoError(t, err)
	assert.True(t, running)

	missing, err := r.IsTaskRunning(ctx, 999999)
	require.NoError(t, err)
	assert.False(t, missing, "任务不存在时返回 false，不报错")
}
