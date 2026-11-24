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

// loginReadyToken 管理员创建的账号首登必须改密（P1-4 起服务端强制，未改密访问业务接口返回
// 403 AUTH_007）。需要访问业务接口的用例统一走这里：登录 → 改密 → 返回改密后的新 token，
// 与真实用户流一致（不靠削弱中间件让测试变绿）。
func loginReadyToken(t *testing.T, ts *httptest.Server, username, initial, newPassword string) string {
	t.Helper()
	token := loginToken(t, ts, username, initial)
	sc, body := post(t, ts, "/api/v1/auth/password", token,
		map[string]any{"old_password": initial, "new_password": newPassword})
	require.Equal(t, 200, sc, body)
	newToken, _ := body["token"].(string)
	require.NotEmpty(t, newToken)
	return newToken
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
	assert.InDelta(t, 0, body["document_count"], 0.001)

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
	devToken := loginReadyToken(t, h.ts, "dev", "dev-pass-123", "dev-pass-456")
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
	devToken := loginReadyToken(t, h.ts, "dev", "dev-pass-123", "dev-pass-456")

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

// TestKBMemberPathParamBounds 成员路径参数非法 → 404（不泄露存在性）。
func TestKBMemberPathParamBounds(t *testing.T) {
	h := setup(t)
	_, _ = post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken := loginToken(t, h.ts, "root", "password8")
	sc, body := post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "边界库"})
	require.Equal(t, 201, sc, body)
	kbID := int64(body["id"].(float64))

	// 非数字 / 非正数 userID → 404
	sc, _ = del(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members/abc", rootToken)
	assert.Equal(t, 404, sc)
	sc, _ = del(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members/0", rootToken)
	assert.Equal(t, 404, sc)
	sc, _ = patch(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members/abc", rootToken, map[string]any{"role": "editor"})
	assert.Equal(t, 404, sc)

	// 角色绑定失败 → 422
	sc, _ = patch(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members/5", rootToken, "not-json")
	assert.Equal(t, 422, sc)
}
