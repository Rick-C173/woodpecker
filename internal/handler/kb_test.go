package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func del(t *testing.T, ts *httptest.Server, path, token string) (int, map[string]any) {
	t.Helper()
	return do(t, ts, http.MethodDelete, path, token, nil)
}

func loginToken(t *testing.T, ts *httptest.Server, username, password string) string {
	t.Helper()
	sc, body := post(t, ts, "/api/v1/auth/login", "", map[string]any{"username": username, "password": password})
	require.Equal(t, 200, sc, body)
	token, _ := body["token"].(string)
	require.NotEmpty(t, token)
	return token
}

func TestKBAdminCRUDAndVisibility(t *testing.T) {
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)

	// 建库
	sc, body := post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "平台文档", "description": "内部平台"})
	require.Equal(t, 201, sc, body)
	kbID := int64(body["id"].(float64))
	assert.Equal(t, "平台文档", body["name"])
	assert.Equal(t, "admin", body["my_role"])
	assert.Equal(t, float64(0), body["document_count"])

	// 重名 → 409 KB_001
	sc, body = post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "平台文档"})
	assert.Equal(t, 409, sc)
	assert.Equal(t, "KB_001", jcode(t, body))

	// 空名 / 超长名 → 422
	sc, body = post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": ""})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))
	sc, body = post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": strings.Repeat("a", 51)})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	// 管理员列表与详情
	sc, body = get(t, h.ts, "/api/v1/kbs", rootToken)
	require.Equal(t, 200, sc)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)
	first, _ := items[0].(map[string]any)
	assert.Equal(t, "admin", first["my_role"])

	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID), rootToken)
	require.Equal(t, 200, sc)
	assert.Equal(t, "平台文档", body["name"])
	// 管理员非成员访问 → 记审计
	assert.GreaterOrEqual(t, h.env.Audit.CountAction("kb.admin_access"), 1)

	// 编辑
	sc, body = patch(t, h.ts, "/api/v1/kbs/"+i64(kbID), rootToken, map[string]any{"name": "平台文档-改", "description": "新描述"})
	require.Equal(t, 200, sc, body)
	assert.Equal(t, "平台文档-改", body["name"])
	assert.Equal(t, "新描述", body["description"])

	// 非法 body / 超长名 / 重名
	sc, body = patch(t, h.ts, "/api/v1/kbs/"+i64(kbID), rootToken, "not-json")
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))
	sc, _ = patch(t, h.ts, "/api/v1/kbs/"+i64(kbID), rootToken, map[string]any{"name": strings.Repeat("b", 51)})
	assert.Equal(t, 422, sc)
	_, _ = post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "另一个库"})
	sc, body = patch(t, h.ts, "/api/v1/kbs/"+i64(kbID), rootToken, map[string]any{"name": "另一个库"})
	assert.Equal(t, 409, sc)
	assert.Equal(t, "KB_001", jcode(t, body))

	// 普通用户：建库 403；列表为空；非成员访问详情 404（不泄露库名）
	_, body = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "dev", "password": "dev-pass-123"})
	require.NotNil(t, body["id"])
	devToken := loginToken(t, h.ts, "dev", "dev-pass-123")
	sc, body = post(t, h.ts, "/api/v1/kbs", devToken, map[string]any{"name": "越权库"})
	assert.Equal(t, 403, sc)
	assert.Equal(t, "FORBIDDEN", jcode(t, body))
	sc, body = get(t, h.ts, "/api/v1/kbs", devToken)
	require.Equal(t, 200, sc)
	assert.Empty(t, body["items"])
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID), devToken)
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", jcode(t, body))
	assert.NotContains(t, body["message"], "平台文档")

	// 删库 → 再次访问 404
	sc, body = del(t, h.ts, "/api/v1/kbs/"+i64(kbID), rootToken)
	require.Equal(t, 200, sc, body)
	sc, _ = get(t, h.ts, "/api/v1/kbs/"+i64(kbID), rootToken)
	assert.Equal(t, 404, sc)
}

func TestKBMembersFlows(t *testing.T) {
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)
	_, body = post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "权限测试库"})
	kbID := int64(body["id"].(float64))

	_, body = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "dev", "password": "dev-pass-123"})
	devID := int64(body["id"].(float64))
	devToken := loginToken(t, h.ts, "dev", "dev-pass-123")

	// 初始成员为空
	sc, body := get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members", rootToken)
	require.Equal(t, 200, sc)
	assert.Empty(t, body["items"])

	// 添加成员（editor）
	sc, body = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members", rootToken,
		map[string]any{"user_id": devID, "role": "editor"})
	require.Equal(t, 201, sc, body)
	assert.Equal(t, "editor", body["role"])

	// 成员列表含用户名
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members", rootToken)
	require.Equal(t, 200, sc)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)
	m, _ := items[0].(map[string]any)
	assert.Equal(t, "dev", m["username"])
	assert.Equal(t, "editor", m["role"])

	// 成员视角：列表 my_role=editor，详情可见
	sc, body = get(t, h.ts, "/api/v1/kbs", devToken)
	require.Equal(t, 200, sc)
	items, _ = body["items"].([]any)
	require.Len(t, items, 1)
	view, _ := items[0].(map[string]any)
	assert.Equal(t, "editor", view["my_role"])
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID), devToken)
	require.Equal(t, 200, sc)
	assert.Equal(t, "editor", body["my_role"])

	// 成员无权管理成员
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members", devToken)
	assert.Equal(t, 403, sc)
	assert.Equal(t, "FORBIDDEN", jcode(t, body))

	// 角色变更 owner
	sc, body = patch(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members/"+i64(devID), rootToken, map[string]any{"role": "owner"})
	require.Equal(t, 200, sc, body)
	_, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID), devToken)
	assert.Equal(t, "owner", body["my_role"])

	// 非法角色 / 用户不存在 / 成员不存在
	sc, body = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members", rootToken, map[string]any{"user_id": devID, "role": "boss"})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))
	sc, body = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members", rootToken, map[string]any{"user_id": 9999, "role": "member"})
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", jcode(t, body))
	sc, _ = patch(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members/abc", rootToken, map[string]any{"role": "member"})
	assert.Equal(t, 404, sc)
	sc, body = patch(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members/9999", rootToken, map[string]any{"role": "member"})
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", jcode(t, body))

	// 移除成员 → 立即失访
	sc, body = del(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members/"+i64(devID), rootToken)
	require.Equal(t, 200, sc, body)
	sc, _ = get(t, h.ts, "/api/v1/kbs/"+i64(kbID), devToken)
	assert.Equal(t, 404, sc)
	sc, body = del(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members/"+i64(devID), rootToken)
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", jcode(t, body))

	// 虚假库 ID（管理员越权 → 服务层仍返回 404）
	sc, _ = get(t, h.ts, "/api/v1/kbs/9999/members", rootToken)
	assert.Equal(t, 404, sc)
}
