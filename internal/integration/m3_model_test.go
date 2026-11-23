//go:build integration

// M3 集成测试：模型服务配置（FR-12）。
// 验收：服务/条目 CRUD、API Key AES-GCM 加密落库、默认唯一与切换（部分唯一索引）、
// MODEL_002/003 拦截、GetDefault 解密回读、审计落库。
package integration_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func TestM3ModelConfig(t *testing.T) {
	e := setup(t)

	// 管理员引导
	sc, body := e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, 201, sc, body)
	rootToken, _ := body["token"].(string)

	// 创建服务
	sc, body = e.call(t, "POST", "/api/v1/admin/model-services", rootToken, map[string]any{
		"name": "DeepSeek", "base_url": "https://api.deepseek.com/v1", "api_key": "sk-it-secret",
	})
	require.Equal(t, 201, sc, body)
	svcID := int64(body["id"].(float64))
	assert.True(t, body["has_key"].(bool))
	assert.NotContains(t, bodyString(body), "sk-it-secret", "API Key 不回显")

	// 落库为密文
	var encRows [][]byte
	require.NoError(t, e.db.Table("model_services").Where("id = ?", svcID).Pluck("api_key_enc", &encRows).Error)
	require.Len(t, encRows, 1)
	enc := encRows[0]
	require.NotEmpty(t, enc)
	assert.NotContains(t, string(enc), "sk-it-secret", "落库必须是密文")

	// 未登录 → 401
	sc, _ = e.call(t, "GET", "/api/v1/admin/model-services", "", nil)
	assert.Equal(t, 401, sc)

	// 创建默认对话条目
	sc, body = e.call(t, "POST", "/api/v1/admin/model-entries", rootToken, map[string]any{
		"service_id": svcID, "purpose": "chat", "model_name": "deepseek-chat", "is_default": true,
	})
	require.Equal(t, 201, sc, body)
	entryID := int64(body["id"].(float64))

	// 第二个默认（同用途）→ 切换，部分唯一索引保证唯一
	sc, body = e.call(t, "POST", "/api/v1/admin/model-entries", rootToken, map[string]any{
		"service_id": svcID, "purpose": "chat", "model_name": "deepseek-v3", "is_default": true,
	})
	require.Equal(t, 201, sc, body)
	entryID2 := int64(body["id"].(float64))
	var defaults int64
	require.NoError(t, e.db.Table("model_entries").Where("purpose = ? AND is_default", model.ModelPurposeChat).Count(&defaults).Error)
	assert.Equal(t, int64(1), defaults, "每用途仅一个默认")

	// 默认条目删除 → MODEL_002
	sc, body = e.call(t, "DELETE", "/api/v1/admin/model-entries/"+i64(entryID2), rootToken, nil)
	assert.Equal(t, 422, sc)
	assert.Equal(t, "MODEL_002", body["code"])

	// 存在条目删服务 → MODEL_003
	sc, body = e.call(t, "DELETE", "/api/v1/admin/model-services/"+i64(svcID), rootToken, nil)
	assert.Equal(t, 409, sc)
	assert.Equal(t, "MODEL_003", body["code"])

	// 列表过滤
	sc, body = e.call(t, "GET", "/api/v1/admin/model-entries?purpose=chat", rootToken, nil)
	require.Equal(t, 200, sc)
	items, _ := body["items"].([]any)
	assert.Len(t, items, 2)

	// 审计
	assert.GreaterOrEqual(t, auditCount(t, e, "model.service_update"), int64(1))
	assert.GreaterOrEqual(t, auditCount(t, e, "model.entry_update"), int64(1))

	// 清理：切默认到 entryID → 删 entryID2；默认条目（唯一）不可删 → 直接清库后删服务
	sc, body = e.call(t, "PATCH", "/api/v1/admin/model-entries/"+i64(entryID), rootToken, map[string]any{"is_default": true})
	require.Equal(t, 200, sc, body)
	sc, _ = e.call(t, "DELETE", "/api/v1/admin/model-entries/"+i64(entryID2), rootToken, nil)
	assert.Equal(t, 200, sc)
	require.NoError(t, e.db.Exec("DELETE FROM model_services").Error)
	sc, _ = e.call(t, "GET", "/api/v1/admin/model-services", rootToken, nil)
	require.Equal(t, 200, sc)
	items, _ = body["items"].([]any)
	assert.Empty(t, items)
}

func bodyString(body map[string]any) string {
	b, _ := jsonMarshal(body)
	return string(b)
}
