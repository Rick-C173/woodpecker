//go:build integration

// M15 集成测试（FR-20 个人 API Token）：创建/列表/机器凭证访问/改密不影响/吊销失效/
// 禁用即时失效/数量上限/越权不可见。环境准备见 setup_test.go。
package integration_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedToken 管理员引导 + 普通用户（已改密就绪）；返回环境与双方 JWT。
func seedToken(t *testing.T) (e *env, rootToken, devToken string, devID int64) {
	t.Helper()
	e = setup(t)
	sc, body := e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, 201, sc, body)
	rootToken, _ = body["token"].(string)

	sc, body = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "dev", "password": "dev-pass-123"})
	require.Equal(t, 201, sc, body)
	devID = int64(body["id"].(float64))
	devToken = e.loginReady(t, "dev", "dev-pass-123", "dev-pass-456")
	return
}

func TestM15APIToken(t *testing.T) {
	e, rootToken, devToken, devID := seedToken(t)

	// 1. 创建：明文仅本次返回，格式 wpk_<64hex>
	sc, body := e.call(t, "POST", "/api/v1/tokens", devToken, map[string]any{"name": "ci 部署"})
	require.Equal(t, 201, sc, body)
	item := body["item"].(map[string]any)
	tokenID := int64(item["id"].(float64))
	plain, _ := body["token"].(string)
	require.Len(t, plain, 68)
	require.True(t, strings.HasPrefix(plain, "wpk_"))
	assert.Equal(t, strings.TrimPrefix(plain, "wpk_")[:8], item["prefix"])
	assert.EqualValues(t, 1, e.count(t, "SELECT count(*) FROM audit_logs WHERE action='token.create'"))

	// 2. 列表：不含明文/哈希
	sc, body = e.call(t, "GET", "/api/v1/tokens", devToken, nil)
	require.Equal(t, 200, sc)
	items := body["items"].([]any)
	require.Len(t, items, 1)
	it0 := items[0].(map[string]any)
	assert.Equal(t, "ci 部署", it0["name"])
	assert.NotContains(t, body, "token_hash")

	// 3. 机器凭证访问业务 API（无需改密流程；dev 无库 → 空列表但认证通过）
	sc, body = e.call(t, "GET", "/api/v1/kbs", plain, nil)
	require.Equal(t, 200, sc, body)
	assert.Empty(t, body["items"])

	// 4. 首登未改密的初始密码会话（AUTH_07 状态）不能创建 token——机器凭证不受改密约束，但人要先改密
	sc, body = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "bob", "password": "bob-pass-123"})
	require.Equal(t, 201, sc, body)
	sc, bobLogin := e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": "bob", "password": "bob-pass-123"})
	require.Equal(t, 200, sc, bobLogin)
	bobTok, _ := bobLogin["token"].(string)
	sc, body = e.call(t, "POST", "/api/v1/tokens", bobTok, map[string]any{"name": "t"})
	assert.Equal(t, 403, sc)
	assert.Equal(t, "AUTH_07", code(t, body))

	// 5. 改密 bump token_version 后：旧 JWT 失效，API Token 仍有效；重新登录续用新 JWT
	sc, _ = e.call(t, "POST", "/api/v1/auth/password", devToken, map[string]any{"old_password": "dev-pass-456", "new_password": "dev-pass-789"})
	require.Equal(t, 200, sc)
	sc, body = e.call(t, "GET", "/api/v1/kbs", devToken, nil)
	assert.Equal(t, 401, sc, "旧 JWT 应失效")
	sc, body = e.call(t, "GET", "/api/v1/kbs", plain, nil)
	require.Equal(t, 200, sc, body, "API Token 不受 token_version 影响")
	sc, devLogin := e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": "dev", "password": "dev-pass-789"})
	require.Equal(t, 200, sc, devLogin)
	devToken, _ = devLogin["token"].(string)

	// 6. 越权吊销：dev 吊销 root 的 token → 404（不泄露存在性）
	sc, body = e.call(t, "POST", "/api/v1/tokens", rootToken, map[string]any{"name": "root-ci"})
	require.Equal(t, 201, sc, body)
	rootItem := body["item"].(map[string]any)
	rootTokenID := int64(rootItem["id"].(float64))
	sc, body = e.call(t, "DELETE", "/api/v1/tokens/"+i64(rootTokenID), devToken, nil)
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", code(t, body))

	// 7. 本人吊销 → 明文即失效（TOK_002）
	sc, body = e.call(t, "DELETE", "/api/v1/tokens/"+i64(tokenID), devToken, nil)
	require.Equal(t, 200, sc, body)
	sc, body = e.call(t, "GET", "/api/v1/kbs", plain, nil)
	assert.Equal(t, 401, sc)
	assert.Equal(t, "TOK_002", code(t, body))
	assert.EqualValues(t, 1, e.count(t, "SELECT count(*) FROM audit_logs WHERE action='token.revoke'"))

	// 8. 禁用用户 → token 即时失效（AUTH_003）
	sc, body = e.call(t, "POST", "/api/v1/tokens", devToken, map[string]any{"name": "post-disable"})
	require.Equal(t, 201, sc, body)
	plain2 := body["token"].(string)
	sc, body = e.call(t, "PATCH", "/api/v1/admin/users/"+i64(devID), rootToken, map[string]any{"status": 2})
	require.Equal(t, 200, sc, body)
	sc, body = e.call(t, "GET", "/api/v1/kbs", plain2, nil)
	assert.Equal(t, 401, sc)
	assert.Equal(t, "AUTH_003", code(t, body))
}

func TestM15APITokenLimit(t *testing.T) {
	e, rootToken, _, _ := seedToken(t)
	for i := 0; i < 20; i++ {
		sc, body := e.call(t, "POST", "/api/v1/tokens", rootToken, map[string]any{"name": "t"})
		require.Equal(t, 201, sc, body)
	}
	sc, body := e.call(t, "POST", "/api/v1/tokens", rootToken, map[string]any{"name": "overflow"})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "TOK_001", code(t, body))

	// 有效期越界
	sc, body = e.call(t, "POST", "/api/v1/tokens", rootToken, map[string]any{"name": "t", "expires_in_days": 0})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", code(t, body))
}
