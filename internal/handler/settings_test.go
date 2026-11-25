package handler_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/repo"
)

func newAdminEnv(t *testing.T) (*hEnv, string) {
	t.Helper()
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	token, _ := body["token"].(string)
	require.NotEmpty(t, token)
	return h, token
}

func TestSettingsAPI(t *testing.T) {
	h, token := newAdminEnv(t)

	// 默认值
	sc, body := get(t, h.ts, "/api/v1/admin/settings", token)
	require.Equal(t, 200, sc, body)
	assert.Equal(t, false, body["self_register"])
	assert.Equal(t, false, body["exclusion_rules_customized"])

	// 更新：两项
	sc, body = patch(t, h.ts, "/api/v1/admin/settings", token, map[string]any{
		"self_register":   true,
		"exclusion_rules": []string{"*.secret", " drafts/ ", ""},
	})
	require.Equal(t, 200, sc, body)

	sc, body = get(t, h.ts, "/api/v1/admin/settings", token)
	require.Equal(t, 200, sc)
	assert.Equal(t, true, body["self_register"])
	rules, _ := body["exclusion_rules"].([]any)
	require.Len(t, rules, 2)
	assert.Equal(t, "drafts/", rules[1], "清洗首尾空白")

	// 审计：每项一次 setting.update
	assert.Equal(t, 2, h.env.Audit.CountAction("setting.update"))

	// 空请求体 → 422
	sc, body = patch(t, h.ts, "/api/v1/admin/settings", token, map[string]any{})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	// 非管理员 → 403
	_, _ = post(t, h.ts, "/api/v1/admin/users", token, map[string]any{"username": "staff", "password": "staff-pass-1"})
	staffToken := loginReadyToken(t, h.ts, "staff", "staff-pass-1", "staff-pass-2")
	sc, _ = get(t, h.ts, "/api/v1/admin/settings", staffToken)
	assert.Equal(t, 403, sc)

	// 自助注册生效：PATCH 开启后 register 不再 403 AUTH_005
	sc, _ = post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "newby", "password": "newby-pass-1"})
	assert.Equal(t, 201, sc, "开启自助注册后可注册")
}

func TestAuditLogsAPI(t *testing.T) {
	h, token := newAdminEnv(t)

	// 产生审计：登录失败 + 建库
	_, _ = post(t, h.ts, "/api/v1/auth/login", "", map[string]any{"username": "root", "password": "wrong-wrong"})
	sc, kb := post(t, h.ts, "/api/v1/kbs", token, map[string]any{"name": "审计库"})
	require.Equal(t, 201, sc, kb)

	// 全量查询（分页信封）：register 记审计（user.register，首管理员引导），共 register + login_failed + kb.create
	sc, body := get(t, h.ts, "/api/v1/admin/audit-logs?page=1&page_size=10", token)
	require.Equal(t, 200, sc, body)
	assert.InDelta(t, 3, body["total"], 0.001)
	items, _ := body["items"].([]any)
	require.Len(t, items, 3)
	first, _ := items[0].(map[string]any)
	assert.Equal(t, "kb.create", first["action"], "新→旧排序")

	// action 筛选
	sc, body = get(t, h.ts, "/api/v1/admin/audit-logs?action=auth.login_failed", token)
	require.Equal(t, 200, sc)
	items, _ = body["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "auth.login_failed", items[0].(map[string]any)["action"])

	// 无匹配 → 空态
	sc, body = get(t, h.ts, "/api/v1/admin/audit-logs?action=never.exists", token)
	require.Equal(t, 200, sc)
	assert.InDelta(t, 0, body["total"], 0.001)
	assert.Empty(t, body["items"])

	// 非法时间格式 → 422
	sc, _ = get(t, h.ts, "/api/v1/admin/audit-logs?from=not-a-time", token)
	assert.Equal(t, 422, sc)

	// 合法时间筛选（to 边界排他）
	now := time.Now().UTC().Format(time.RFC3339)
	sc, body = get(t, h.ts, "/api/v1/admin/audit-logs?to="+now, token)
	require.Equal(t, 200, sc)
	assert.InDelta(t, 0, body["total"], 0.001, "to 排他：全部日志晚于该时刻")
}

func TestFeedbacksAPI(t *testing.T) {
	h, token := newAdminEnv(t)
	h.feedbackRepo.Rows = []repo.FeedbackAdminRow{
		{ID: 1, MessageID: 11, ConvID: 5, KBID: 1, Reason: 1, Username: "alice", MessageText: "回答一", CreatedAt: time.Now().Add(-time.Hour)},
		{ID: 2, MessageID: 12, ConvID: 5, KBID: 1, Reason: 4, Username: "bob", MessageText: "回答二", CreatedAt: time.Now()},
		{ID: 3, MessageID: 13, ConvID: 6, KBID: 2, Reason: 2, Username: "carol", MessageText: "回答三", CreatedAt: time.Now()},
	}

	// 全量
	sc, body := get(t, h.ts, "/api/v1/admin/feedbacks", token)
	require.Equal(t, 200, sc, body)
	assert.InDelta(t, 3, body["total"], 0.001)

	// kb + reason 组合筛选
	sc, body = get(t, h.ts, "/api/v1/admin/feedbacks?kb_id=1&reason=4", token)
	require.Equal(t, 200, sc)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)
	row, _ := items[0].(map[string]any)
	assert.InDelta(t, float64(2), row["id"], 0.001)
	assert.Equal(t, "bob", row["username"])
	assert.Equal(t, "回答二", row["message_text"])

	// 分页
	sc, body = get(t, h.ts, "/api/v1/admin/feedbacks?page=2&page_size=2", token)
	require.Equal(t, 200, sc)
	items, _ = body["items"].([]any)
	assert.Len(t, items, 1)

	// 未登录 → 401
	sc, _ = get(t, h.ts, "/api/v1/admin/feedbacks", "")
	assert.Equal(t, 401, sc)
}
