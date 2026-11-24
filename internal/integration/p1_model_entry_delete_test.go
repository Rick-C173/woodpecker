//go:build integration

// P1-7 真库回归：跑过问答的模型条目必须可删。
//
// 000001_init 的 messages.model_entry_id 外键未指定 ON DELETE（默认 NO ACTION），
// 只要条目被任何 assistant 消息引用过，DELETE /admin/model-entries/{id} 就触发外键冲突，
// 被 FailServiceError 兜成 500「服务内部错误」——旧条目永远删不掉。
// 000004 迁移改为 ON DELETE SET NULL 后：删除成功（200），消息仍在、model_entry_id 置空。
package integration_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestP1MessageModelEntryOnDeleteSetNull(t *testing.T) {
	e := setup(t)

	// 管理员引导 + 建库
	sc, body := e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, http.StatusCreated, sc, body)
	rootToken, _ := body["token"].(string)
	rootID := int64(body["user_id"].(float64))

	sc, body = e.call(t, "POST", "/api/v1/kbs", rootToken, map[string]any{"name": "溯源库"})
	require.Equal(t, http.StatusCreated, sc, body)
	kbID := int64(body["id"].(float64))

	// 模型服务 + 非默认条目（默认条目会被 MODEL_002 拦住，测不到外键行为）
	sc, body = e.call(t, "POST", "/api/v1/admin/model-services", rootToken, map[string]any{
		"name": "DeepSeek", "base_url": "https://api.deepseek.com/v1", "api_key": "sk-it",
	})
	require.Equal(t, http.StatusCreated, sc, body)
	svcID := int64(body["id"].(float64))
	sc, body = e.call(t, "POST", "/api/v1/admin/model-entries", rootToken, map[string]any{
		"service_id": svcID, "purpose": "chat", "model_name": "deepseek-chat",
	})
	require.Equal(t, http.StatusCreated, sc, body)
	entryID := int64(body["id"].(float64))

	// 一条引用了该条目的 assistant 消息（模拟"跑过一次问答"后的溯源记录）
	var convID, msgID int64
	require.NoError(t, e.db.Raw(
		`INSERT INTO conversations (kb_id, user_id, title) VALUES (?, ?, ?) RETURNING id`,
		kbID, rootID, "溯源会话").Scan(&convID).Error)
	require.NotZero(t, convID)
	require.NoError(t, e.db.Raw(
		`INSERT INTO messages (conversation_id, role, content, model_entry_id) VALUES (?, 2, ?, ?) RETURNING id`,
		convID, "回答内容", entryID).Scan(&msgID).Error)
	require.NotZero(t, msgID)

	// 删除条目：修复前此处为 500「服务内部错误」，修复后 200
	sc, body = e.call(t, "DELETE", "/api/v1/admin/model-entries/"+i64(entryID), rootToken, nil)
	require.Equal(t, http.StatusOK, sc, body)

	// 消息仍在，但溯源引用被置空（ON DELETE SET NULL）
	var row struct {
		ID           int64
		ModelEntryID *int64
	}
	require.NoError(t, e.db.Raw(
		`SELECT id, model_entry_id FROM messages WHERE id = ?`, msgID).Scan(&row).Error)
	assert.Equal(t, msgID, row.ID, "消息不应因条目删除而消失")
	assert.Nil(t, row.ModelEntryID, "model_entry_id 应为 NULL")

	// 条目确已删除
	var entryCount int64
	require.NoError(t, e.db.Table("model_entries").Where("id = ?", entryID).Count(&entryCount).Error)
	assert.Equal(t, int64(0), entryCount)
}
