//go:build integration

// M11 集成测试（FR-17 可见性 + owner 授权）：
//   - org 库：非成员列表可见（my_role=org）、详情可读、成员管理 403；切回私有立即 404
//   - 私有库：非成员保持 404（不可见原则）；PATCH 仅管理员
//   - owner 可授权：owner 添加成员/改角色；owner 不能变更/移除自己（403）；editor 无授权权
package integration_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestM11VisibilityAndOwnerGrant(t *testing.T) {
	e := setup(t)

	// 1. bootstrap + 建库（私有缺省 / org 显式）
	sc, sb := e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, 201, sc, sb)
	rootToken, _ := sb["token"].(string)

	sc, sb = e.call(t, "POST", "/api/v1/kbs", rootToken, map[string]any{"name": "私有项目库"})
	require.Equal(t, 201, sc, sb)
	privKB := int64(sb["id"].(float64))
	assert.EqualValues(t, 1, sb["visibility"], "缺省=私有")

	sc, sb = e.call(t, "POST", "/api/v1/kbs", rootToken, map[string]any{"name": "公司规范库", "visibility": 2})
	require.Equal(t, 201, sc, sb)
	orgKB := int64(sb["id"].(float64))
	assert.EqualValues(t, 2, sb["visibility"])
	sc, sb = e.call(t, "POST", "/api/v1/kbs", rootToken, map[string]any{"name": "非法库", "visibility": 3})
	assert.Equal(t, 422, sc, "visibility 取值仅 1|2")

	// 2. 管理员建两个普通用户
	sc, sb = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "dev"})
	require.Equal(t, 201, sc, sb)
	devToken := e.loginReady(t, "dev", sb["initial_password"].(string), "devpass88")
	devID := int64(sb["id"].(float64))
	sc, sb = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "eve"})
	require.Equal(t, 201, sc, sb)
	eveInitial := sb["initial_password"].(string)
	eveID := int64(sb["id"].(float64))

	// 3. 非成员视角：列表只见 org 库（my_role=org）
	sc, sb = e.call(t, "GET", "/api/v1/kbs", devToken, nil)
	require.Equal(t, 200, sc, sb)
	items, _ := sb["items"].([]any)
	require.Len(t, items, 1, "普通用户列表=成员库∪org库")
	item, _ := items[0].(map[string]any)
	assert.Equal(t, "公司规范库", item["name"])
	assert.Equal(t, "org", item["my_role"])
	assert.EqualValues(t, 2, item["visibility"])

	// 4. org 库详情可读；私有库 404（不可见原则）
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(orgKB), devToken, nil)
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, "org", sb["my_role"])
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(privKB), devToken, nil)
	assert.Equal(t, 404, sc)

	// 5. org 访客写操作被拦：PATCH 库 403（Admin）；成员管理 403（KBMemberManager）
	sc, sb = e.call(t, "PATCH", "/api/v1/kbs/"+i64(orgKB), devToken, map[string]any{"name": "劫持"})
	assert.Equal(t, 403, sc)
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(orgKB)+"/members", devToken, map[string]any{"user_id": eveID, "role": "member"})
	assert.Equal(t, 403, sc)
	assert.Equal(t, "FORBIDDEN", code(t, sb))

	// 6. 可见性切换立即生效：org → 私有 → 非成员 404
	sc, sb = e.call(t, "PATCH", "/api/v1/kbs/"+i64(orgKB), rootToken, map[string]any{"visibility": 1})
	require.Equal(t, 200, sc, sb)
	assert.EqualValues(t, 1, sb["visibility"])
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(orgKB), devToken, nil)
	assert.Equal(t, 404, sc, "切回私有立即生效")
	sc, sb = e.call(t, "GET", "/api/v1/kbs", devToken, nil)
	require.Equal(t, 200, sc)
	items, _ = sb["items"].([]any)
	assert.Empty(t, items, "列表不再包含该库")
	// 切回 org 恢复
	sc, sb = e.call(t, "PATCH", "/api/v1/kbs/"+i64(orgKB), rootToken, map[string]any{"visibility": 2})
	require.Equal(t, 200, sc, sb)

	// 7. owner 授权：root 设 dev 为私有库管理者
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(privKB)+"/members", rootToken, map[string]any{"user_id": devID, "role": "owner"})
	require.Equal(t, 201, sc, sb)

	// 8. owner 可添加成员与变更角色
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(privKB)+"/members", devToken, map[string]any{"user_id": eveID, "role": "editor"})
	require.Equal(t, 201, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(privKB)+"/members", devToken, nil)
	require.Equal(t, 200, sc, sb, "owner 可查看成员列表")
	// owner 变更 eve 的角色
	sc, sb = e.call(t, "PATCH", "/api/v1/kbs/"+i64(privKB)+"/members/"+i64(eveID), devToken, map[string]any{"role": "member"})
	require.Equal(t, 200, sc, sb)

	// 9. owner 自我保护：不能变更/移除自己（403）
	sc, sb = e.call(t, "PATCH", "/api/v1/kbs/"+i64(privKB)+"/members/"+i64(devID), devToken, map[string]any{"role": "member"})
	assert.Equal(t, 403, sc)
	assert.Contains(t, sb["message"], "自己的角色")
	sc, sb = e.call(t, "DELETE", "/api/v1/kbs/"+i64(privKB)+"/members/"+i64(devID), devToken, nil)
	assert.Equal(t, 403, sc)
	assert.Contains(t, sb["message"], "移除自己")

	// 10. editor 无授权权（R6）
	eveToken := e.loginReady(t, "eve", eveInitial, "evepass88")
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(privKB)+"/members", eveToken, map[string]any{"user_id": eveID, "role": "member"})
	assert.Equal(t, 403, sc, "editor 不能管理成员")

	// 11. 审计：visibility 变更与成员授权均落审计
	n := e.count(t, "SELECT count(*) FROM audit_logs WHERE action = 'kb.update' AND detail::text LIKE '%visibility%'")
	assert.GreaterOrEqual(t, n, int64(2), "两次可见性切换应记审计")
	n = e.count(t, "SELECT count(*) FROM audit_logs WHERE action IN ('member.grant','member.role_change')")
	assert.GreaterOrEqual(t, n, int64(3))
}
