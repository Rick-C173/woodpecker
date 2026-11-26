//go:build integration

// M12 集成测试（FR-18 用户组与组授权）：
// 组管理（唯一名 409 GROUP_001）、组员增删（404/409 GROUP_002）、组授权 CRUD（409 GROUP_003）、
// effectiveRole 优先级（直授覆盖组授、组授 owner 获管理权）、即时生效（移出组/撤销授权/删组）、
// owner 自我保护、权限边界与审计。
package integration_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// token 按约定密码 <name>pass88 登录取 token（本文件测试用户）。
func (e *env) token(t *testing.T, username string) string {
	t.Helper()
	sc, body := e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": username, "password": username + "pass88"})
	require.Equal(t, 200, sc, body)
	tk, _ := body["token"].(string)
	require.NotEmpty(t, tk)
	return tk
}

func TestM12Groups(t *testing.T) {
	e := setup(t)

	// 1. bootstrap + 测试用户（密码均约定为 <name>pass88）
	sc, sb := e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, 201, sc, sb)
	rootToken, _ := sb["token"].(string)

	users := map[string]int64{}
	for _, name := range []string{"u1", "u2", "u3", "u4"} {
		sc, sb = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": name})
		require.Equal(t, 201, sc, sb)
		users[name] = int64(sb["id"].(float64))
		e.loginReady(t, name, sb["initial_password"].(string), name+"pass88")
	}

	// 2. 建私有库
	sc, sb = e.call(t, "POST", "/api/v1/kbs", rootToken, map[string]any{"name": "组测库"})
	require.Equal(t, 201, sc, sb)
	kb := int64(sb["id"].(float64))

	// 3. 组管理：建组 + 重名 409 GROUP_001
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups", rootToken, map[string]any{"name": "后端组", "description": "Go 后端"})
	require.Equal(t, 201, sc, sb)
	g1 := int64(sb["id"].(float64))
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups", rootToken, map[string]any{"name": "后端组"})
	assert.Equal(t, 409, sc)
	assert.Equal(t, "GROUP_001", code(t, sb))
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups", rootToken, map[string]any{"name": "运维组"})
	require.Equal(t, 201, sc, sb)
	g2 := int64(sb["id"].(float64))
	sc, sb = e.call(t, "GET", "/api/v1/admin/groups", rootToken, nil)
	require.Equal(t, 200, sc, sb)
	list, _ := sb["items"].([]any)
	assert.Len(t, list, 2)

	// 4. 组员管理：不存在 404 / 加入 / 重复 409 GROUP_002 / 列表 / 移除后可重加
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups/"+i64(g1)+"/members", rootToken, map[string]any{"user_id": 99999})
	assert.Equal(t, 404, sc)
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups/"+i64(g1)+"/members", rootToken, map[string]any{"user_id": users["u1"]})
	require.Equal(t, 201, sc, sb)
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups/"+i64(g1)+"/members", rootToken, map[string]any{"user_id": users["u1"]})
	assert.Equal(t, 409, sc)
	assert.Equal(t, "GROUP_002", code(t, sb))
	sc, sb = e.call(t, "GET", "/api/v1/admin/groups/"+i64(g1)+"/members", rootToken, nil)
	require.Equal(t, 200, sc, sb)
	items, _ := sb["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "u1", items[0].(map[string]any)["username"])
	sc, sb = e.call(t, "DELETE", "/api/v1/admin/groups/"+i64(g1)+"/members/"+i64(users["u1"]), rootToken, nil)
	require.Equal(t, 200, sc, sb)
	sc, sb = e.call(t, "DELETE", "/api/v1/admin/groups/"+i64(g1)+"/members/"+i64(users["u1"]), rootToken, nil)
	assert.Equal(t, 404, sc, "移除不存在的成员")
	// u1 重新加入组1（后续生效用例用）
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups/"+i64(g1)+"/members", rootToken, map[string]any{"user_id": users["u1"]})
	require.Equal(t, 201, sc, sb)

	// 5. 组授权：member / owner；重复 409 GROUP_003
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(kb)+"/group-roles", rootToken, map[string]any{"group_id": g1, "role": "member"})
	require.Equal(t, 201, sc, sb)
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(kb)+"/group-roles", rootToken, map[string]any{"group_id": g1, "role": "editor"})
	assert.Equal(t, 409, sc)
	assert.Equal(t, "GROUP_003", code(t, sb))
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(kb)+"/group-roles", rootToken, map[string]any{"group_id": g2, "role": "owner"})
	require.Equal(t, 201, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(kb)+"/group-roles", rootToken, nil)
	require.Equal(t, 200, sc, sb)
	grants, _ := sb["items"].([]any)
	assert.Len(t, grants, 2)

	// 6. 生效：u1（组1 member，无直授）可读；u4 未授权 404
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(kb), e.token(t, "u1"), nil)
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, "member", sb["my_role"], "组授 member 生效")
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(kb), e.token(t, "u4"), nil)
	assert.Equal(t, 404, sc, "未授权用户不可见")

	// 7. 优先级：u1 直授 editor + 组授 member → editor（直授覆盖）
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(kb)+"/members", rootToken, map[string]any{"user_id": users["u1"], "role": "editor"})
	require.Equal(t, 201, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(kb), e.token(t, "u1"), nil)
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, "editor", sb["my_role"], "直授 editor 高于组授 member")

	// 8. u2 加入组2（owner）→ 组授 owner 生效：可管理成员 + 自我保护
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups/"+i64(g2)+"/members", rootToken, map[string]any{"user_id": users["u2"]})
	require.Equal(t, 201, sc, sb)
	u2Token := e.token(t, "u2")
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(kb), u2Token, nil)
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, "owner", sb["my_role"], "组授 owner 生效")
	sc, sb = e.call(t, "PATCH", "/api/v1/kbs/"+i64(kb)+"/members/"+i64(users["u2"]), u2Token, map[string]any{"role": "member"})
	assert.Equal(t, 403, sc, "经组获得的 owner 同样不能变更自己")
	sc, sb = e.call(t, "DELETE", "/api/v1/kbs/"+i64(kb)+"/members/"+i64(users["u2"]), u2Token, nil)
	assert.Equal(t, 403, sc, "经组获得的 owner 同样不能移除自己")

	// 9. 即时失效：u1 移出组1 → 组授 member 失效，直授 editor 仍在
	sc, sb = e.call(t, "DELETE", "/api/v1/admin/groups/"+i64(g1)+"/members/"+i64(users["u1"]), rootToken, nil)
	require.Equal(t, 200, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(kb), e.token(t, "u1"), nil)
	require.Equal(t, 200, sc, sb)
	assert.Equal(t, "editor", sb["my_role"], "移出组后直授角色仍在")

	// 10. 撤销组授权：u2 即时失访
	sc, sb = e.call(t, "DELETE", "/api/v1/kbs/"+i64(kb)+"/group-roles/"+i64(g2), rootToken, nil)
	require.Equal(t, 200, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(kb), u2Token, nil)
	assert.Equal(t, 404, sc, "撤销组授权即时失访")

	// 11. u3 加入组1（member）→ 可读；删组 → 级联失访 + 授权清除
	sc, sb = e.call(t, "POST", "/api/v1/admin/groups/"+i64(g1)+"/members", rootToken, map[string]any{"user_id": users["u3"]})
	require.Equal(t, 201, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(kb), e.token(t, "u3"), nil)
	require.Equal(t, 200, sc, sb, "u3 经组1 可读")
	sc, sb = e.call(t, "DELETE", "/api/v1/admin/groups/"+i64(g1), rootToken, nil)
	require.Equal(t, 200, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/kbs/"+i64(kb), e.token(t, "u3"), nil)
	assert.Equal(t, 404, sc, "删组后 u3 失访")
	assert.Equal(t, int64(0), e.count(t, "SELECT count(*) FROM kb_group_roles WHERE group_id = "+i64(g1)), "删组级联清除授权")

	// 12. 权限边界：普通用户不能管理组；editor（u1）不能组授权
	sc, sb = e.call(t, "GET", "/api/v1/admin/groups", e.token(t, "u4"), nil)
	assert.Equal(t, 403, sc)
	sc, sb = e.call(t, "POST", "/api/v1/kbs/"+i64(kb)+"/group-roles", e.token(t, "u1"), map[string]any{"group_id": g2, "role": "member"})
	assert.Equal(t, 403, sc)

	// 13. 审计
	n := e.count(t, "SELECT count(*) FROM audit_logs WHERE action LIKE 'group.%'")
	assert.GreaterOrEqual(t, n, int64(4))
	n = e.count(t, "SELECT count(*) FROM audit_logs WHERE action LIKE 'kb.group_%'")
	assert.GreaterOrEqual(t, n, int64(3))
}
