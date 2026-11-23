//go:build integration

// M6 集成测试：会话/消息/引用/反馈仓储（FR-07/08/09/14）真库验证。
// 验收：CRUD、引用替换、反馈唯一覆盖与撤销、删除会话级联清理。
package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

func TestM6ConversationRepo(t *testing.T) {
	db := openTestDB(t)
	truncateAll(db)
	ctx := context.Background()

	// 用户 + 库
	var userID int64
	require.NoError(t, db.Raw(`INSERT INTO users (username, password_hash, is_admin, status) VALUES ('m6root', 'x', TRUE, 1) RETURNING id`).Scan(&userID).Error)
	kb := &model.KnowledgeBase{Name: "问答库", CreatedBy: userID}
	require.NoError(t, db.Create(kb).Error)

	r := repo.NewConversationRepo(db)

	// 会话 CRUD
	c := &model.Conversation{KBID: kb.ID, UserID: userID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, r.CreateConversation(ctx, c))
	require.Greater(t, c.ID, int64(0))
	got, err := r.GetConversationByID(ctx, c.ID)
	require.NoError(t, err)
	assert.Equal(t, kb.ID, got.KBID)
	assert.Equal(t, userID, got.UserID)

	// 消息落库（user + assistant）
	u := &model.Message{ConversationID: c.ID, Role: model.MsgRoleUser, Content: "什么是限流", CreatedAt: time.Now()}
	require.NoError(t, r.CreateMessage(ctx, u))
	a := &model.Message{ConversationID: c.ID, Role: model.MsgRoleAssistant, Content: "限流是...", CreatedAt: time.Now()}
	require.NoError(t, r.CreateMessage(ctx, a))
	require.Greater(t, a.ID, u.ID, "消息 ID 递增")

	// 引用替换（先插 2 条再替换为 1 条）
	cs := []model.MessageCitation{
		{MessageID: a.ID, DocumentID: 1, Seq: 1, ChunkID: 11, FilePath: ptrStr6("a.go"), Snippet: ptrStr6("片段一")},
		{MessageID: a.ID, DocumentID: 1, Seq: 2, ChunkID: 12, FilePath: ptrStr6("b.go"), Snippet: ptrStr6("片段二")},
	}
	require.NoError(t, r.ReplaceCitations(ctx, a.ID, cs))
	require.Equal(t, int64(2), countCitations(t, db, a.ID))
	cs2 := []model.MessageCitation{
		{MessageID: a.ID, DocumentID: 2, Seq: 1, ChunkID: 99, FilePath: ptrStr6("c.go"), Snippet: ptrStr6("新片段")},
	}
	require.NoError(t, r.ReplaceCitations(ctx, a.ID, cs2))
	require.Equal(t, int64(1), countCitations(t, db, a.ID), "旧引用被清理")

	// 消息历史含引用
	msgs, err := r.ListMessages(ctx, c.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, model.MsgRoleUser, msgs[0].Role)
	require.Len(t, msgs[1].Citations, 1)
	assert.Equal(t, int64(99), msgs[1].Citations[0].ChunkID)
	require.NotNil(t, msgs[1].Citations[0].Snippet)
	assert.Equal(t, "新片段", *msgs[1].Citations[0].Snippet)

	// LastMessages 正序尾部
	last, err := r.LastMessages(ctx, c.ID, 1)
	require.NoError(t, err)
	require.Len(t, last, 1)
	assert.Equal(t, a.ID, last[0].ID, "取最近 1 条为 assistant 回答")

	// 反馈：upsert 覆盖 → 查询 → 撤销
	fb := &model.Feedback{MessageID: a.ID, UserID: userID, Reason: 1, CreatedAt: time.Now()}
	require.NoError(t, r.UpsertFeedback(ctx, fb))
	fb2 := &model.Feedback{MessageID: a.ID, UserID: userID, Reason: 4, Comment: ptrStr6("换个说法"), CreatedAt: time.Now()}
	require.NoError(t, r.UpsertFeedback(ctx, fb2))
	assert.Equal(t, int64(1), m6count(t, db, "SELECT COUNT(*) FROM feedbacks"), "UNIQUE(message_id,user_id) 覆盖而非新增")
	row, err := r.GetFeedbackByUser(ctx, a.ID, userID)
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, int16(4), row.Reason)
	require.NoError(t, r.DeleteFeedback(ctx, a.ID, userID))
	row, err = r.GetFeedbackByUser(ctx, a.ID, userID)
	require.NoError(t, err)
	assert.Nil(t, row)
	require.ErrorIs(t, r.DeleteFeedback(ctx, a.ID, userID), repo.ErrNotFound, "撤销无记录 → NotFound")

	// 我的会话列表（message_count 子查询）
	rows, err := r.ListMine(ctx, kb.ID, userID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(2), rows[0].MessageCount)

	// 删除会话级联：messages/citations/feedbacks 清空
	require.NoError(t, r.UpsertFeedback(ctx, fb))
	require.NoError(t, r.DeleteConversation(ctx, c.ID))
	assert.Equal(t, int64(0), m6count(t, db, "SELECT COUNT(*) FROM conversations"))
	assert.Equal(t, int64(0), m6count(t, db, "SELECT COUNT(*) FROM messages"))
	assert.Equal(t, int64(0), m6count(t, db, "SELECT COUNT(*) FROM message_citations"))
	assert.Equal(t, int64(0), m6count(t, db, "SELECT COUNT(*) FROM feedbacks"))

	// 不存在映射
	_, err = r.GetConversationByID(ctx, 99999)
	require.ErrorIs(t, err, repo.ErrNotFound)
}

func ptrStr6(s string) *string { return &s }

func m6count(t *testing.T, db *gormDB, sql string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(sql).Scan(&n).Error)
	return n
}

func countCitations(t *testing.T, db *gormDB, messageID int64) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM message_citations WHERE message_id = ?`, messageID).Scan(&n).Error)
	return n
}
