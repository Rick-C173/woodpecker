//go:build integration

// DocumentRepo / SearchRepo / ConversationRepo 真库集成测试（M4-M6 仓储方法分支）。
package repo

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

func TestDocumentRepoIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r := NewDocumentRepo(itDB)
	kbs := NewKBRepo(itDB)
	users := NewUserRepo(itDB)
	owner := itSeedUser(t, users, "docowner", true)
	kb := &model.KnowledgeBase{Name: "文档库", CreatedBy: owner.ID}
	require.NoError(t, kbs.Create(ctx, kb))

	doc := &model.Document{KBID: kb.ID, Type: model.DocTypeGit, Name: "order-service",
		Status: model.DocStatusProcessing, CreatedBy: owner.ID}
	require.NoError(t, r.CreateDocument(ctx, doc))
	require.Greater(t, doc.ID, int64(0))
	got, err := r.GetDocumentByID(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, model.DocStatusProcessing, got.Status)
	_, err = r.GetDocumentByID(ctx, 99999)
	require.ErrorIs(t, err, ErrNotFound)
	n, err := r.CountByKB(ctx, kb.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	rows, total, err := r.ListDocuments(ctx, DocumentListQuery{KBID: kb.ID, Type: model.DocTypeGit, Status: model.DocStatusProcessing, Page: 1, PerPage: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)

	f1 := &model.DocumentFile{DocumentID: doc.ID, Path: "docs/api.md", Status: 1}
	require.NoError(t, r.UpsertFile(ctx, f1))
	files, err := r.ListFiles(ctx, doc.ID)
	require.NoError(t, err)
	require.Len(t, files, 1)
	fg, err := r.GetFileByID(ctx, f1.ID)
	require.NoError(t, err)
	assert.Equal(t, "docs/api.md", fg.Path)
	require.NoError(t, r.DeleteFilesByPath(ctx, doc.ID, []string{"docs/api.md"}))
	files, _ = r.ListFiles(ctx, doc.ID)
	assert.Empty(t, files)

	chunks := []model.Chunk{
		{DocumentID: doc.ID, KBID: kb.ID, Content: "块一", ChunkIndex: 0, FilePath: itStr("docs/api.md"), Embedding: itVec(0.9)},
		{DocumentID: doc.ID, KBID: kb.ID, Content: "块二", ChunkIndex: 1, FilePath: itStr("docs/other.md"), Embedding: itVec(0.2)},
	}
	require.NoError(t, r.ReplaceDocumentChunks(ctx, doc.ID, chunks))
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+strconv.FormatInt(doc.ID, 10)))
	require.NoError(t, r.DeleteChunksByFile(ctx, doc.ID, "docs/api.md"))
	assert.Equal(t, int64(1), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+strconv.FormatInt(doc.ID, 10)))

	// InsertChunks：按文件追加（chunk_index 为文件内序号，唯一键含 file_path）
	require.NoError(t, r.InsertChunks(ctx, doc.ID, []model.Chunk{
		{DocumentID: doc.ID, KBID: kb.ID, Content: "块一（重切）", ChunkIndex: 0, FilePath: itStr("docs/api.md"), Embedding: itVec(0.5)},
		{DocumentID: doc.ID, KBID: kb.ID, Content: "块一之二", ChunkIndex: 1, FilePath: itStr("docs/api.md"), Embedding: itVec(0.4)},
	}))
	assert.Equal(t, int64(3), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+strconv.FormatInt(doc.ID, 10)))
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM chunks WHERE document_id = "+strconv.FormatInt(doc.ID, 10)+" AND file_path = 'docs/api.md'"))

	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull, Status: model.TaskStatusQueued}
	require.NoError(t, r.CreateTask(ctx, task))
	running, err := r.HasRunningTask(ctx, doc.ID)
	require.NoError(t, err)
	assert.True(t, running)
	other := &model.Document{KBID: kb.ID, Type: model.DocTypeUpload, Name: "a.md", Status: model.DocStatusReady, CreatedBy: owner.ID}
	require.NoError(t, r.CreateDocument(ctx, other))
	running, err = r.HasRunningTask(ctx, other.ID)
	require.NoError(t, err)
	assert.False(t, running)
	task.Status = model.TaskStatusSuccess
	require.NoError(t, r.SaveTask(ctx, task))
	gotTask, err := r.GetTaskByID(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, gotTask.Status)
	hist, err := r.ListTaskHistory(ctx, doc.ID, 10)
	require.NoError(t, err)
	require.Len(t, hist, 1)
	assert.Equal(t, model.TaskStatusSuccess, hist[0].Status)

	z1 := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull, Status: model.TaskStatusRunning}
	require.NoError(t, r.CreateTask(ctx, z1))
	n, err = r.FailZombieTasks(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	n, err = r.FailProcessingDocs(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "processing 文档置失败")
	got, _ = r.GetDocumentByID(ctx, doc.ID)
	assert.Equal(t, model.DocStatusFailed, got.Status)

	require.NoError(t, r.DeleteDocument(ctx, doc.ID))
	_, err = r.GetDocumentByID(ctx, doc.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

// TestSearchRepoIT 混合检索 SQL（M5）：向量近似 + trigram 关键词融合与库隔离。
func TestSearchRepoIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	users := NewUserRepo(itDB)
	owner := itSeedUser(t, users, "m5owner", true)
	kbs := NewKBRepo(itDB)
	kb := &model.KnowledgeBase{Name: "检索库", CreatedBy: owner.ID}
	require.NoError(t, kbs.Create(ctx, kb))
	kb2 := &model.KnowledgeBase{Name: "隔离库", CreatedBy: owner.ID}
	require.NoError(t, kbs.Create(ctx, kb2))
	r := NewDocumentRepo(itDB)
	doc := &model.Document{KBID: kb.ID, Type: model.DocTypeUpload, Name: "m5", Status: model.DocStatusReady, CreatedBy: owner.ID}
	require.NoError(t, r.CreateDocument(ctx, doc))
	sr := NewSearchRepo(itDB)

	seed := func(kbID int64, idx int, content, filePath string, first float64) {
		vec := make([]float32, 1024)
		vec[0] = float32(first)
		vec[1] = float32(1 - first)
		var id int64
		require.NoError(t, itDB.Raw(`INSERT INTO chunks (document_id, kb_id, content, chunk_index, file_path, line_start, line_end, embedding)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?::vector) RETURNING id`,
			doc.ID, kbID, content, idx, filePath, 10, 20, pgvector.NewVector(vec)).Scan(&id).Error)
	}
	// A：向量+关键词双命中；B：仅近似；C：正交向量（都不该命中）
	seed(kb.ID, 0, "限流中间件实现，使用固定窗口算法", "internal/middleware/ratelimit.go", 0.95)
	seed(kb.ID, 1, "完全不同的内容关于部署流程", "docs/deploy.md", 0.05)
	ortho := make([]float32, 1024)
	ortho[500] = 1
	var cid int64
	require.NoError(t, itDB.Raw(`INSERT INTO chunks (document_id, kb_id, content, chunk_index, file_path, line_start, line_end, embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?::vector) RETURNING id`,
		doc.ID, kb.ID, "无关内容其它", 2, "other/x.txt", 10, 20, pgvector.NewVector(ortho)).Scan(&cid).Error)
	// 隔离库：同名关键词不应跨库命中
	seed(kb2.ID, 3, "另一个库的限流实现", "other/x.go", 0.9)

	qvec := make([]float32, 1024)
	qvec[0] = 0.9
	qvec[1] = 0.1
	hits, err := sr.HybridSearch(ctx, kb.ID, qvec, "限流", 50)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, "internal/middleware/ratelimit.go", *hits[0].FilePath, "向量+关键词双命中排第一")
	for _, h := range hits {
		assert.NotEqual(t, "other/x.txt", *h.FilePath, "正交向量块不应命中")
	}

	// 空态
	far := make([]float32, 1024)
	far[600] = 1
	hits, err = sr.HybridSearch(ctx, kb.ID, far, "不存在关键词xyz", 50)
	require.NoError(t, err)
	assert.Empty(t, hits)

	// CountReadyChunks
	n, err := sr.CountReadyChunks(ctx, kb.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
}

func TestConversationRepoIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r := NewConversationRepo(itDB)
	users := NewUserRepo(itDB)
	owner := itSeedUser(t, users, "m6owner", true)
	kbs := NewKBRepo(itDB)
	kb := &model.KnowledgeBase{Name: "问答库", CreatedBy: owner.ID}
	require.NoError(t, kbs.Create(ctx, kb))

	c := &model.Conversation{KBID: kb.ID, UserID: owner.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, r.CreateConversation(ctx, c))
	require.Greater(t, c.ID, int64(0))
	got, err := r.GetConversationByID(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, kb.ID, got.KBID)

	u := &model.Message{ConversationID: c.ID, Role: model.MsgRoleUser, Content: "什么是限流", CreatedAt: time.Now()}
	require.NoError(t, r.CreateMessage(ctx, u))
	a := &model.Message{ConversationID: c.ID, Role: model.MsgRoleAssistant, Content: "限流是...", CreatedAt: time.Now()}
	require.NoError(t, r.CreateMessage(ctx, a))
	require.Greater(t, a.ID, u.ID, "消息 ID 递增")

	cs := []model.MessageCitation{
		{MessageID: a.ID, DocumentID: 1, Seq: 1, ChunkID: 11, FilePath: itStr("a.go"), Snippet: itStr("片段一")},
		{MessageID: a.ID, DocumentID: 1, Seq: 2, ChunkID: 12, FilePath: itStr("b.go"), Snippet: itStr("片段二")},
	}
	require.NoError(t, r.ReplaceCitations(ctx, a.ID, cs))
	assert.Equal(t, int64(2), itCount(t, "SELECT COUNT(*) FROM message_citations WHERE message_id = "+strconv.FormatInt(a.ID, 10)))
	cs2 := []model.MessageCitation{
		{MessageID: a.ID, DocumentID: 2, Seq: 1, ChunkID: 99, FilePath: itStr("c.go"), Snippet: itStr("新片段")},
	}
	require.NoError(t, r.ReplaceCitations(ctx, a.ID, cs2))
	assert.Equal(t, int64(1), itCount(t, "SELECT COUNT(*) FROM message_citations WHERE message_id = "+strconv.FormatInt(a.ID, 10)), "旧引用被清理")

	msgs, err := r.ListMessages(ctx, c.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, model.MsgRoleUser, msgs[0].Role)
	require.Len(t, msgs[1].Citations, 1)
	assert.Equal(t, int64(99), msgs[1].Citations[0].ChunkID)
	require.NotNil(t, msgs[1].Citations[0].Snippet)
	assert.Equal(t, "新片段", *msgs[1].Citations[0].Snippet)

	last, err := r.LastMessages(ctx, c.ID, 1)
	require.NoError(t, err)
	require.Len(t, last, 1)
	assert.Equal(t, a.ID, last[0].ID, "取最近 1 条为 assistant 回答")

	fb := &model.Feedback{MessageID: a.ID, UserID: owner.ID, Reason: 1, CreatedAt: time.Now()}
	require.NoError(t, r.UpsertFeedback(ctx, fb))
	fb2 := &model.Feedback{MessageID: a.ID, UserID: owner.ID, Reason: 4, Comment: itStr("换个说法"), CreatedAt: time.Now()}
	require.NoError(t, r.UpsertFeedback(ctx, fb2))
	assert.Equal(t, int64(1), itCount(t, "SELECT COUNT(*) FROM feedbacks"), "UNIQUE(message_id,user_id) 覆盖而非新增")
	row, err := r.GetFeedbackByUser(ctx, a.ID, owner.ID)
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, int16(4), row.Reason)
	require.NoError(t, r.DeleteFeedback(ctx, a.ID, owner.ID))
	row, err = r.GetFeedbackByUser(ctx, a.ID, owner.ID)
	require.NoError(t, err)
	assert.Nil(t, row)
	require.ErrorIs(t, r.DeleteFeedback(ctx, a.ID, owner.ID), ErrNotFound, "撤销无记录 → NotFound")

	rows2, err := r.ListMine(ctx, kb.ID, owner.ID)
	require.NoError(t, err)
	require.Len(t, rows2, 1)
	assert.Equal(t, int64(2), rows2[0].MessageCount)

	require.NoError(t, r.UpsertFeedback(ctx, fb))
	require.NoError(t, r.DeleteConversation(ctx, c.ID))
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM conversations"))
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM messages"))
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM message_citations"))
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM feedbacks"))

	_, err = r.GetConversationByID(ctx, 99999)
	require.ErrorIs(t, err, ErrNotFound)
}

func itVec(first float64) []float32 {
	v := make([]float32, 1024)
	v[0] = float32(first)
	v[1] = float32(1 - first)
	return v
}
