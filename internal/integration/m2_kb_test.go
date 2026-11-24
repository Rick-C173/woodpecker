//go:build integration

// M2 集成测试：知识库与成员权限（FR-02 / R6）。
// 验收：四角色可见性与操作边界、非成员 404 不泄露库名、级联删除、审计落库。
package integration_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func auditCount(t *testing.T, e *env, action string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, e.db.Table("audit_logs").Where("action = ?", action).Count(&n).Error)
	return n
}

func TestM2KnowledgeBaseAndMembers(t *testing.T) {
	e := setup(t)

	// 管理员引导 + 普通用户
	sc, body := e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, 201, sc, body)
	rootToken, _ := body["token"].(string)

	sc, body = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "dev", "password": "dev-pass-123"})
	require.Equal(t, 201, sc, body)
	devID := int64(body["id"].(float64))

	sc, body = e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": "dev", "password": "dev-pass-123"})
	require.Equal(t, 200, sc, body)
	assert.Equal(t, true, body["must_change_password"], "管理员创建的账号首登必须改密")
	// 首登未改密 → 业务接口 403 AUTH_007（审计 P1-4）
	devInitialToken, _ := body["token"].(string)
	sc, body = e.call(t, "GET", "/api/v1/kbs", devInitialToken, nil)
	assert.Equal(t, http.StatusForbidden, sc)
	assert.Equal(t, "AUTH_007", code(t, body))
	// 改密后正常访问
	devToken := e.loginReady(t, "dev", "dev-pass-123", "dev-pass-456")

	// 建库（管理员）+ 重名 409
	sc, body = e.call(t, "POST", "/api/v1/kbs", rootToken, map[string]any{"name": "订单服务文档", "description": "M2 集成测试"})
	require.Equal(t, 201, sc, body)
	kbID := int64(body["id"].(float64))
	sc, body = e.call(t, "POST", "/api/v1/kbs", rootToken, map[string]any{"name": "订单服务文档"})
	assert.Equal(t, 409, sc)
	assert.Equal(t, "KB_001", body["code"])
	assert.Equal(t, int64(1), auditCount(t, e, "kb.create"))

	// 非成员：列表不可见、详情 404、不能建库
	sc, body = e.call(t, "GET", "/api/v1/kbs", devToken, nil)
	require.Equal(t, 200, sc)
	assert.Empty(t, body["items"])
	sc, body = e.call(t, "GET", "/api/v1/kbs/"+i64(kbID), devToken, nil)
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", body["code"])
	sc, body = e.call(t, "POST", "/api/v1/kbs", devToken, map[string]any{"name": "越权库"})
	assert.Equal(t, 403, sc)

	// 授权为编辑者 → 可见且 my_role=editor
	sc, body = e.call(t, "POST", "/api/v1/kbs/"+i64(kbID)+"/members", rootToken,
		map[string]any{"user_id": devID, "role": "editor"})
	require.Equal(t, 201, sc, body)
	sc, body = e.call(t, "GET", "/api/v1/kbs", devToken, nil)
	require.Equal(t, 200, sc)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)
	view, _ := items[0].(map[string]any)
	assert.Equal(t, "editor", view["my_role"])
	assert.Equal(t, float64(0), view["document_count"])

	sc, body = e.call(t, "GET", "/api/v1/kbs/"+i64(kbID), devToken, nil)
	require.Equal(t, 200, sc)
	assert.Equal(t, "订单服务文档", body["name"])
	assert.Equal(t, "editor", body["my_role"])

	// 成员无权管理成员
	sc, _ = e.call(t, "GET", "/api/v1/kbs/"+i64(kbID)+"/members", devToken, nil)
	assert.Equal(t, 403, sc)

	// 成员列表（含用户名）+ 角色变更
	sc, body = e.call(t, "GET", "/api/v1/kbs/"+i64(kbID)+"/members", rootToken, nil)
	require.Equal(t, 200, sc)
	members, _ := body["items"].([]any)
	require.Len(t, members, 1)
	m0, _ := members[0].(map[string]any)
	assert.Equal(t, "dev", m0["username"])

	sc, body = e.call(t, "PATCH", "/api/v1/kbs/"+i64(kbID)+"/members/"+i64(devID), rootToken, map[string]any{"role": "owner"})
	require.Equal(t, 200, sc, body)
	sc, body = e.call(t, "GET", "/api/v1/kbs/"+i64(kbID), devToken, nil)
	require.Equal(t, 200, sc)
	assert.Equal(t, "owner", body["my_role"])
	assert.Equal(t, int64(1), auditCount(t, e, "member.grant"))
	assert.Equal(t, int64(1), auditCount(t, e, "member.role_change"))

	// 管理员非成员访问 → 审计
	sc, _ = e.call(t, "GET", "/api/v1/kbs/"+i64(kbID), rootToken, nil)
	require.Equal(t, 200, sc)
	assert.GreaterOrEqual(t, auditCount(t, e, "kb.admin_access"), int64(1))

	// 移除成员 → 立即失访
	sc, _ = e.call(t, "DELETE", "/api/v1/kbs/"+i64(kbID)+"/members/"+i64(devID), rootToken, nil)
	require.Equal(t, 200, sc)
	sc, _ = e.call(t, "GET", "/api/v1/kbs/"+i64(kbID), devToken, nil)
	assert.Equal(t, 404, sc)
	assert.Equal(t, int64(1), auditCount(t, e, "member.remove"))

	// 删库（级联）→ 404
	sc, _ = e.call(t, "DELETE", "/api/v1/kbs/"+i64(kbID), rootToken, nil)
	require.Equal(t, 200, sc)
	sc, _ = e.call(t, "GET", "/api/v1/kbs/"+i64(kbID), rootToken, nil)
	assert.Equal(t, 404, sc)
	assert.Equal(t, int64(1), auditCount(t, e, "kb.delete"))

	var leftover int64
	require.NoError(t, e.db.Table("kb_members").Count(&leftover).Error)
	assert.Zero(t, leftover, "删库应级联清理成员")
}
