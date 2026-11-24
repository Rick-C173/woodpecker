//go:build integration

// 上传接入的事务与回滚真库集成测试（审计 P1-9）。
package repo

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

// TestCreateDocumentWithTaskRollbackIT P1-9：建任务失败时文档必须整体回滚，
// 不留「有文档没任务」的待处理僵尸文档（Retry 只接受 failed，用户重试不了）。
func TestCreateDocumentWithTaskRollbackIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r, kb, _ := itSeedDocKB(t)

	// payload 是 jsonb 列，写入非法 JSON 必然失败 → 事务回滚
	d := &model.Document{KBID: kb.ID, Type: model.DocTypeUpload, Name: "rollback.md",
		Status: model.DocStatusPending, CreatedBy: 1}
	bad := &model.IngestTask{Type: model.TaskTypeFull, Status: model.TaskStatusQueued,
		Payload: []byte("{不是 JSON")}
	require.Error(t, r.CreateDocumentWithTask(ctx, d, bad), "任务写入失败应返回错误")
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM documents WHERE name = 'rollback.md'"),
		"文档应随事务回滚，不留待处理僵尸文档")
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM ingest_tasks"))

	// 正常路径：文档与任务同时落库（用全新结构体：上面的失败尝试已把 d.ID 回填）
	okDoc := &model.Document{KBID: kb.ID, Type: model.DocTypeUpload, Name: "rollback-ok.md",
		Status: model.DocStatusPending, CreatedBy: 1}
	good := &model.IngestTask{Type: model.TaskTypeFull, Status: model.TaskStatusQueued}
	require.NoError(t, r.CreateDocumentWithTask(ctx, okDoc, good))
	assert.Equal(t, okDoc.ID, good.DocumentID, "任务已挂到同一事务创建的文档")
	assert.Equal(t, int64(1), itCount(t, "SELECT COUNT(*) FROM documents WHERE name = 'rollback-ok.md'"))
	assert.Equal(t, int64(1),
		itCount(t, "SELECT COUNT(*) FROM ingest_tasks WHERE document_id = "+strconv.FormatInt(okDoc.ID, 10)))
}
