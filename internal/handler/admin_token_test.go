package handler_test

// 管理员 token 失效回归（审计 P1-2）：设/撤管理员必须 bump token_version，
// 否则已撤销权限的管理员凭旧 token 仍可越权，最长持续 JWT_TTL_HOURS（默认 24h）。

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRevokeAdminInvalidatesToken 撤权立即生效（审计 P1-2 / PRD R6）：
// 设/撤管理员都会 bump token_version，旧 token（claims 里的 adm 已过期）必须 401 AUTH_004，
// 而不是靠中间件每请求回查 DB。
func TestRevokeAdminInvalidatesToken(t *testing.T) {
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)

	// 建一个普通用户并改密（清除首登标记，排除 AUTH_007 干扰）
	sc, body := post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "ops", "password": "ops-pass-8"})
	require.Equal(t, 201, sc, body)
	opsID := int64(body["id"].(float64))
	_, body = post(t, h.ts, "/api/v1/auth/login", "", map[string]any{"username": "ops", "password": "ops-pass-8"})
	opsToken := body["token"].(string)
	sc, _ = post(t, h.ts, "/api/v1/auth/password", opsToken, map[string]any{"old_password": "ops-pass-8", "new_password": "ops-new-pass"})
	require.Equal(t, 200, sc)
	_, body = post(t, h.ts, "/api/v1/auth/login", "", map[string]any{"username": "ops", "password": "ops-new-pass"})
	prePromotionToken := body["token"].(string)

	// 设为管理员：旧 token（adm=false）同样失效
	sc, body = patch(t, h.ts, "/api/v1/admin/users/"+i64(opsID), rootToken, map[string]any{"is_admin": true})
	require.Equal(t, 200, sc, body)
	sc, body = get(t, h.ts, "/api/v1/admin/users", prePromotionToken)
	assert.Equal(t, 401, sc, "提升管理员后旧 token 应失效")
	assert.Equal(t, "AUTH_004", jcode(t, body))

	// 重新登录拿到带 adm=true 的 token → 可访问管理端点
	_, body = post(t, h.ts, "/api/v1/auth/login", "", map[string]any{"username": "ops", "password": "ops-new-pass"})
	adminToken := body["token"].(string)
	sc, _ = get(t, h.ts, "/api/v1/admin/users", adminToken)
	require.Equal(t, 200, sc)

	// 撤销管理员 → 旧 token 立即失效（修复前仍可越权，最长持续 JWT_TTL_HOURS）
	sc, body = patch(t, h.ts, "/api/v1/admin/users/"+i64(opsID), rootToken, map[string]any{"is_admin": false})
	require.Equal(t, 200, sc, body)
	sc, body = get(t, h.ts, "/api/v1/admin/users", adminToken)
	assert.Equal(t, 401, sc, "撤权后旧 token 必须立即失效")
	assert.Equal(t, "AUTH_004", jcode(t, body))

	// 撤销后重新登录 → 403 FORBIDDEN（已非管理员）
	_, body = post(t, h.ts, "/api/v1/auth/login", "", map[string]any{"username": "ops", "password": "ops-new-pass"})
	opsToken2 := body["token"].(string)
	sc, body = get(t, h.ts, "/api/v1/admin/users", opsToken2)
	assert.Equal(t, 403, sc)
	assert.Equal(t, "FORBIDDEN", jcode(t, body))
}
