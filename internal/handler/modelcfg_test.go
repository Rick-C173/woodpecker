package handler_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func mcSetupHandler(t *testing.T) *hEnv {
	t.Helper()
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)
	require.NotEmpty(t, rootToken)
	h.rootToken = rootToken
	return h
}

// TestMCAdminFlows 模型服务配置全链路（FR-12）。
func TestMCAdminFlows(t *testing.T) {
	h := mcSetupHandler(t)
	base := "/api/v1/admin"

	// 未登录 → 401
	sc, body := get(t, h.ts, base+"/model-services", "")
	assert.Equal(t, 401, sc)
	assert.Equal(t, "AUTH_004", jcode(t, body))

	// 创建服务
	sc, body = post(t, h.ts, base+"/model-services", h.rootToken, map[string]any{
		"name": "DeepSeek", "base_url": "https://api.deepseek.com/v1", "api_key": "sk-secret-1",
	})
	require.Equal(t, 201, sc, body)
	svcID := int64(body["id"].(float64))
	assert.True(t, body["has_key"].(bool))
	assert.NotContains(t, jsonOf(body), "sk-secret-1", "API Key 不回显")

	// 校验：缺字段 → 422
	sc, body = post(t, h.ts, base+"/model-services", h.rootToken, map[string]any{"name": "x"})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	// 列表
	sc, body = get(t, h.ts, base+"/model-services", h.rootToken)
	require.Equal(t, 200, sc)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)
	first, _ := items[0].(map[string]any)
	assert.InDelta(t, 0, first["entry_count"], 0.001)

	// 创建默认对话条目
	sc, body = post(t, h.ts, base+"/model-entries", h.rootToken, map[string]any{
		"service_id": svcID, "purpose": "chat", "model_name": "deepseek-chat",
		"params": map[string]any{"temperature": 0.3}, "is_default": true,
	})
	require.Equal(t, 201, sc, body)
	entryID := int64(body["id"].(float64))
	assert.Equal(t, "chat", body["purpose"])
	assert.True(t, body["is_default"].(bool))

	// 非法 purpose → 422；params 非对象由绑定拦截（数组 → JSON 序列化失败 → 422）
	sc, _ = post(t, h.ts, base+"/model-entries", h.rootToken, map[string]any{
		"service_id": svcID, "purpose": "vision", "model_name": "x",
	})
	assert.Equal(t, 422, sc)

	// 条目列表（含服务名与默认标记）
	sc, body = get(t, h.ts, base+"/model-entries?service_id="+i64(svcID), h.rootToken)
	require.Equal(t, 200, sc)
	items, _ = body["items"].([]any)
	require.Len(t, items, 1)
	e0, _ := items[0].(map[string]any)
	assert.Equal(t, "DeepSeek", e0["service_name"])
	assert.True(t, e0["is_default"].(bool))

	// purpose 过滤非法值 → 422
	sc, _ = get(t, h.ts, base+"/model-entries?purpose=abc", h.rootToken)
	assert.Equal(t, 422, sc)

	// 存在条目时删服务 → 409 MODEL_003
	sc, body = del(t, h.ts, base+"/model-services/"+i64(svcID), h.rootToken)
	assert.Equal(t, 409, sc)
	assert.Equal(t, "MODEL_003", jcode(t, body))

	// 默认条目不可删 → MODEL_002
	sc, body = del(t, h.ts, base+"/model-entries/"+i64(entryID), h.rootToken)
	assert.Equal(t, 422, sc)
	assert.Equal(t, "MODEL_002", jcode(t, body))

	// 追加非默认条目
	sc, body = post(t, h.ts, base+"/model-entries", h.rootToken, map[string]any{
		"service_id": svcID, "purpose": "embedding", "model_name": "bge-m3",
	})
	require.Equal(t, 201, sc, body)
	entry2ID := int64(body["id"].(float64))

	// 测试连接（FakeTester 全 ok）
	sc, body = post(t, h.ts, base+"/model-services/"+i64(svcID)+"/test", h.rootToken, nil)
	require.Equal(t, 200, sc, body)
	assert.Equal(t, "ok", body["chat"])
	assert.Equal(t, "ok", body["embedding"])
	assert.NotContains(t, body, "rerank")

	// 停用持有默认条目的服务 → 422 MODEL_004（审计 P1-6）
	sc, body = patch(t, h.ts, base+"/model-services/"+i64(svcID), h.rootToken, map[string]any{"enabled": false})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "MODEL_004", jcode(t, body))

	// 把默认切到另一个服务后即可停用（否则该用途会整体失去默认）
	sc, body = post(t, h.ts, base+"/model-services", h.rootToken, map[string]any{
		"name": "备用服务", "base_url": "https://api.backup.com/v1", "api_key": "sk-backup",
	})
	require.Equal(t, 201, sc, body)
	backupID := int64(body["id"].(float64))
	sc, body = post(t, h.ts, base+"/model-entries", h.rootToken, map[string]any{
		"service_id": backupID, "purpose": "chat", "model_name": "backup-chat", "is_default": true,
	})
	require.Equal(t, 201, sc, body)
	sc, body = patch(t, h.ts, base+"/model-services/"+i64(svcID), h.rootToken, map[string]any{"enabled": false})
	require.Equal(t, 200, sc, body)

	// 停用服务上直接设默认 → 拒绝
	sc, body = post(t, h.ts, base+"/model-entries", h.rootToken, map[string]any{
		"service_id": svcID, "purpose": "rerank", "model_name": "bge-reranker", "is_default": true,
	})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	// 恢复启用并把默认切回本条（回归后段断言：默认条目仍不可删）
	sc, body = patch(t, h.ts, base+"/model-services/"+i64(svcID), h.rootToken, map[string]any{"enabled": true})
	require.Equal(t, 200, sc, body)
	sc, body = patch(t, h.ts, base+"/model-entries/"+i64(entryID), h.rootToken, map[string]any{"is_default": true})
	require.Equal(t, 200, sc, body)

	// 删除非默认条目 → 删服务
	sc, body = del(t, h.ts, base+"/model-entries/"+i64(entry2ID), h.rootToken)
	require.Equal(t, 200, sc, body)
	// 默认条目仍存在 → 仍 MODEL_003
	sc, _ = del(t, h.ts, base+"/model-services/"+i64(svcID), h.rootToken)
	assert.Equal(t, 409, sc)
	sc, body = del(t, h.ts, base+"/model-entries/"+i64(entryID), h.rootToken)
	assert.Equal(t, 422, sc)
	assert.Equal(t, "MODEL_002", jcode(t, body))

	// 普通用户访问 → 403（由 Admin 中间件拦截）
	_, _ = post(t, h.ts, "/api/v1/admin/users", h.rootToken, map[string]any{"username": "dev", "password": "dev-pass-123"})
	devToken := loginReadyToken(t, h.ts, "dev", "dev-pass-123", "dev-pass-456")
	sc, body = get(t, h.ts, base+"/model-services", devToken)
	assert.Equal(t, 403, sc)
	assert.Equal(t, "FORBIDDEN", jcode(t, body))

	// 路径参数非法 → 404
	sc, _ = patch(t, h.ts, base+"/model-services/abc", h.rootToken, map[string]any{"enabled": true})
	assert.Equal(t, 404, sc)
	sc, _ = del(t, h.ts, base+"/model-entries/abc", h.rootToken)
	assert.Equal(t, 404, sc)
	sc, _ = del(t, h.ts, base+"/model-services/abc", h.rootToken)
	assert.Equal(t, 404, sc)
	sc, _ = post(t, h.ts, base+"/model-services/abc/test", h.rootToken, nil)
	assert.Equal(t, 404, sc)

	// 更新/删除不存在的资源 → 404
	sc, body = patch(t, h.ts, base+"/model-services/9999", h.rootToken, map[string]any{"enabled": true})
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", jcode(t, body))
	sc, _ = del(t, h.ts, base+"/model-services/9999", h.rootToken)
	assert.Equal(t, 404, sc)
	sc, _ = patch(t, h.ts, base+"/model-entries/9999", h.rootToken, map[string]any{"model_name": "x"})
	assert.Equal(t, 404, sc)
	sc, _ = del(t, h.ts, base+"/model-entries/9999", h.rootToken)
	assert.Equal(t, 404, sc)
	sc, _ = post(t, h.ts, base+"/model-services/9999/test", h.rootToken, nil)
	assert.Equal(t, 404, sc)

	// 请求体非法 → 422
	sc, _ = patch(t, h.ts, base+"/model-services/"+i64(svcID), h.rootToken, "not-json")
	assert.Equal(t, 422, sc)
	sc, _ = post(t, h.ts, base+"/model-entries", h.rootToken, map[string]any{"service_id": svcID})
	assert.Equal(t, 422, sc)
	sc, _ = patch(t, h.ts, base+"/model-entries/"+i64(entryID), h.rootToken, "not-json")
	assert.Equal(t, 422, sc)

	// 测试连接失败路径（FakeTester 注入错误）
	h.mcTester.ChatErr = errors.New("模型服务不可用（对话测试）: refused")
	sc, body = post(t, h.ts, base+"/model-services/"+i64(svcID)+"/test", h.rootToken, nil)
	require.Equal(t, 200, sc)
	assert.Contains(t, body["chat"], "err:")
	h.mcTester.ChatErr = nil
}

// TestMCRepoErrorToHTTP 存储层错误 → 500 INTERNAL（不泄露内部细节）。
func TestMCRepoErrorToHTTP(t *testing.T) {
	h := mcSetupHandler(t)
	base := "/api/v1/admin"
	boom := errors.New("db down")

	h.mcRepo.ErrOnList = boom
	sc, body := get(t, h.ts, base+"/model-services", h.rootToken)
	assert.Equal(t, 500, sc)
	assert.Equal(t, "INTERNAL", jcode(t, body))
	sc, _ = get(t, h.ts, base+"/model-entries", h.rootToken)
	assert.Equal(t, 500, sc)
	h.mcRepo.ErrOnList = nil

	// 创建服务绑定失败（缺 base_url / api_key）→ 422
	sc, _ = post(t, h.ts, base+"/model-services", h.rootToken, map[string]any{"name": "x"})
	assert.Equal(t, 422, sc)
	sc, _ = post(t, h.ts, base+"/model-services", h.rootToken, "not-json")
	assert.Equal(t, 422, sc)
}
