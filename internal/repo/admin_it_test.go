//go:build integration

// 管理端查询真库集成（M7）：审计筛选、设置 Upsert、反馈联表（FR-14/15）。
package repo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

func TestAuditListIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r := NewAuditRepo(itDB)
	users := NewUserRepo(itDB)
	u := itSeedUser(t, users, "auditor", true)
	other := itSeedUser(t, users, "someone", false)

	mk := func(userID *int64, action, objType string, ago time.Duration) {
		require.NoError(t, r.Add(ctx, &model.AuditLog{
			UserID: userID, Action: action, ObjectType: objType, IP: "10.0.0.1",
			CreatedAt: time.Now().Add(-ago),
		}))
	}
	mk(&u.ID, "kb.create", "kb", 3*time.Hour)
	mk(&other.ID, "auth.login_failed", "user", 2*time.Hour)
	mk(&u.ID, "user.disable", "user", 1*time.Hour)

	// 全量倒序
	rows, total, err := r.List(ctx, AuditQuery{Page: 1, PerPage: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, rows, 3)
	assert.Equal(t, "user.disable", rows[0].Action, "新→旧")
	assert.NotNil(t, rows[0].Username)
	assert.Equal(t, "auditor", *rows[0].Username, "联表用户名")
	assert.Equal(t, "someone", *rows[1].Username)

	// action 筛选
	rows, total, err = r.List(ctx, AuditQuery{Action: "kb.create"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "kb.create", rows[0].Action)

	// user 筛选
	rows, total, err = r.List(ctx, AuditQuery{UserID: u.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)

	// 组合：object_type + 时间窗（90 分钟窗仅含 ago=1h 的 user.disable）
	from := time.Now().Add(-90 * time.Minute)
	to := time.Now().Add(-30 * time.Minute)
	rows, total, err = r.List(ctx, AuditQuery{ObjectType: "user", From: &from, To: &to})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, "user.disable", rows[0].Action)

	// 分页
	rows, total, err = r.List(ctx, AuditQuery{Page: 2, PerPage: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 1, "第二页只剩 1 条")

	// detail JSON 解析（用独特动作避免与首段 kb.create 混淆）
	require.NoError(t, r.Add(ctx, &model.AuditLog{UserID: &u.ID, Action: "setting.update", ObjectType: "setting",
		Detail: []byte(`{"key":"exclusion_rules"}`)}))
	rows, _, err = r.List(ctx, AuditQuery{Action: "setting.update"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].Detail)
	assert.Equal(t, "exclusion_rules", rows[0].Detail["key"])
}

func TestSettingSetIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r := NewSettingRepo(itDB)
	users := NewUserRepo(itDB)
	admin := itSeedUser(t, users, "setter", true)

	// Upsert 两次：更新而非新增
	require.NoError(t, r.Set(ctx, "exclusion_rules", []byte(`{"patterns":["*.key"]}`), admin.ID))
	require.NoError(t, r.Set(ctx, "exclusion_rules", []byte(`{"patterns":["*.pem","secrets/"]}`), admin.ID))
	assert.Equal(t, int64(1), itCount(t, "SELECT COUNT(*) FROM settings WHERE key = 'exclusion_rules'"))

	raw, err := r.GetString(ctx, "exclusion_rules", "")
	require.NoError(t, err)
	assert.Contains(t, raw, "*.pem")

	// GetBool 默认值语义
	v, err := r.GetBool(ctx, "self_register_enabled", false)
	require.NoError(t, err)
	assert.False(t, v)
	require.NoError(t, r.Set(ctx, "self_register_enabled", []byte("true"), admin.ID))
	v, err = r.GetBool(ctx, "self_register_enabled", false)
	require.NoError(t, err)
	assert.True(t, v)
}

func TestFeedbackListIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	users := NewUserRepo(itDB)
	kbs := NewKBRepo(itDB)
	conv := NewConversationRepo(itDB)
	admin := itSeedUser(t, users, "fbviewer", true)

	kb1 := &model.KnowledgeBase{Name: "库一", CreatedBy: admin.ID}
	require.NoError(t, kbs.Create(ctx, kb1))
	kb2 := &model.KnowledgeBase{Name: "库二", CreatedBy: admin.ID}
	require.NoError(t, kbs.Create(ctx, kb2))

	mk := func(kbID int64, reason int16, ago time.Duration) {
		c := &model.Conversation{KBID: kbID, UserID: admin.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		require.NoError(t, conv.CreateConversation(ctx, c))
		m := &model.Message{ConversationID: c.ID, Role: model.MsgRoleAssistant, Content: "回答文本", CreatedAt: time.Now()}
		require.NoError(t, conv.CreateMessage(ctx, m))
		fb := &model.Feedback{MessageID: m.ID, UserID: admin.ID, Reason: reason, CreatedAt: time.Now().Add(-ago)}
		require.NoError(t, conv.UpsertFeedback(ctx, fb))
	}
	mk(kb1.ID, 1, 3*time.Hour)
	mk(kb1.ID, 4, 2*time.Hour)
	mk(kb2.ID, 2, 1*time.Hour)

	// 全量 + 联表字段
	rows, total, err := conv.ListFeedbacks(ctx, FeedbackQuery{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, rows, 3)
	assert.Equal(t, "fbviewer", rows[0].Username, "联 users")
	assert.Equal(t, "回答文本", rows[0].MessageText, "联 messages")
	assert.NotZero(t, rows[0].KBID, "联 conversations.kb_id")

	// kb + reason 筛选
	rows, total, err = conv.ListFeedbacks(ctx, FeedbackQuery{KBID: kb1.ID, Reason: 4})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Equal(t, int16(4), rows[0].Reason)

	// 时间窗（90 分钟内只有 ago=1h 一条）
	from := time.Now().Add(-90 * time.Minute)
	rows, total, err = conv.ListFeedbacks(ctx, FeedbackQuery{From: &from})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)

	// 分页
	rows, total, err = conv.ListFeedbacks(ctx, FeedbackQuery{Page: 2, PerPage: 2})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, rows, 1)
}
