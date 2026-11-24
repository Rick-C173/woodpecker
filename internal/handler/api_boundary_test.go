package handler_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/service"
)

// 接口边界值 + 等价类测试（API设计.md 全局约定与各端点校验规则）。
// 等价类：合法类（恰等边界）与非法类（越界/空/类型错）；边界值：min-、min、max、max+。

func newBoundaryEnv(t *testing.T) (*hEnv, string) {
	t.Helper()
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	token, _ := body["token"].(string)
	require.NotEmpty(t, token)
	return h, token
}

// ---- 用户名/密码边界（1~64 字节；密码 ≥8） ----

func TestBoundaryCredentials(t *testing.T) {
	h, token := newBoundaryEnv(t)
	path := "/api/v1/admin/users"

	cases := []struct {
		name     string
		username string
		password string
		want     int
	}{
		{"用户名恰 64 字节", strings.Repeat("u", 64), "password8", 201},
		{"用户名 65 字节越界", strings.Repeat("u", 65), "password8", 422},
		{"用户名恰 1 字符", "x", "password8", 201},
		{"用户名为空", "", "password8", 422},
		{"密码恰 8 位", "b8user", "12345678", 201},
		{"密码 7 位越界", "b7user", "1234567", 422},
		{"密码为空", "b0user", "", 201}, // 空密码走随机初始密码（合法类）
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, body := post(t, h.ts, path, token, map[string]any{"username": tc.username, "password": tc.password})
			assert.Equal(t, tc.want, sc, body)
			if tc.want == 422 {
				assert.Equal(t, service.CodeValidation, jcode(t, body))
			}
		})
	}
}

// ---- 知识库名称/描述边界（rune ≤50 / ≤500） ----

func TestBoundaryKBFields(t *testing.T) {
	h, token := newBoundaryEnv(t)
	path := "/api/v1/kbs"

	cases := []struct {
		name    string
		kb      string
		desc    string
		descSet bool
		want    int
	}{
		{"名称恰 50 个中文 rune", strings.Repeat("库", 50), "", false, 201},
		{"名称 51 rune 越界", strings.Repeat("库", 51), "", false, 422},
		{"名称恰 50 字节 ASCII", strings.Repeat("k", 50), "", false, 201},
		{"名称为空", "", "", false, 422},
		{"描述恰 500 rune", strings.Repeat("描", 50), strings.Repeat("述", 500), true, 201},
		{"描述 501 rune 越界", strings.Repeat("描", 50), strings.Repeat("述", 501), true, 422},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := tc.kb
			for seen[name] { // 同长度合法类需唯一名（KB_001 409 会污染边界判定）
				name += "·"
			}
			seen[name] = true
			payload := map[string]any{"name": name}
			if tc.descSet {
				payload["description"] = tc.desc
			}
			sc, body := post(t, h.ts, path, token, payload)
			assert.Equal(t, tc.want, sc, body)
			if tc.want == 422 {
				assert.Equal(t, service.CodeValidation, jcode(t, body))
			}
		})
	}
}

// ---- 会话重命名标题边界（rune ≤200）与提问长度边界（rune ≤4000） ----

func TestBoundaryChatFields(t *testing.T) {
	h, token, kbID := newChatEnv(t)
	sc, body := post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/conversations", token, nil)
	require.Equal(t, 201, sc, body)
	convID := int64(body["id"].(float64))
	convPath := "/api/v1/conversations/" + i64(convID)

	// 标题恰 200 rune 合法
	sc, body = patch(t, h.ts, convPath, token, map[string]any{"title": strings.Repeat("题", 200)})
	assert.Equal(t, 200, sc, body)
	// 201 rune 越界
	sc, body = patch(t, h.ts, convPath, token, map[string]any{"title": strings.Repeat("题", 201)})
	assert.Equal(t, 422, sc)
	assert.Equal(t, service.CodeValidation, jcode(t, body))

	// 提问恰 4000 rune 合法（SSE 200；fake 流式器立返）
	sc, _, raw := postSSE(t, h.ts, convPath+"/messages", token, `{"content":"`+strings.Repeat("问", 4000)+`"}`)
	assert.Equal(t, 200, sc, raw)
	events := parseSSE(t, raw)
	require.NotEmpty(t, events)
	assert.Equal(t, "done", events[len(events)-1].event)

	// 4001 rune 越界（SSE 前置校验 → JSON 422）
	sc, body = post(t, h.ts, convPath+"/messages", token, map[string]any{"content": strings.Repeat("问", 4001)})
	assert.Equal(t, 422, sc)
	assert.Equal(t, service.CodeValidation, jcode(t, body))

	// 等价类：空白字符内容 → 422；类型错误 → 422
	sc, _ = post(t, h.ts, convPath+"/messages", token, map[string]any{"content": "   \n\t "})
	assert.Equal(t, 422, sc)
	sc, _ = post(t, h.ts, convPath+"/messages", token, map[string]any{"content": 123})
	assert.Equal(t, 422, sc)
}

// ---- 检索 q 等价类（空/缺参/正常/超长不崩） ----

func TestBoundarySearchQuery(t *testing.T) {
	h, token := newBoundaryEnv(t)
	// 建库以获得成员资格（search 挂在 /:kb 下）
	sc, kb := post(t, h.ts, "/api/v1/kbs", token, map[string]any{"name": "检索边界库"})
	require.Equal(t, 201, sc, kb)
	kbID := int64(kb["id"].(float64))
	base := "/api/v1/kbs/" + i64(kbID) + "/search"

	// 空 q → 422；缺 q → 422
	sc2, _ := get(t, h.ts, base+"?q=", token)
	assert.Equal(t, 422, sc2)
	sc2, _ = get(t, h.ts, base, token)
	assert.Equal(t, 422, sc2)

	// 正常词（无索引 → 空态 200）
	sc, body := get(t, h.ts, base+"?q=登录", token)
	assert.Equal(t, 200, sc, body)
	assert.InDelta(t, 0, body["total"], 0.001)

	// q 恰 500 字符合法、501 越界（实现校验：≤500）
	sc, body = get(t, h.ts, base+"?q="+strings.Repeat("词", 500), token)
	assert.Equal(t, 200, sc, body)
	sc, body = get(t, h.ts, base+"?q="+strings.Repeat("词", 501), token)
	assert.Equal(t, 422, sc)
	assert.Equal(t, service.CodeValidation, jcode(t, body))
}

// ---- 审计时间窗边界（from==to 空窗、from>to 逆序、非法格式） ----

func TestBoundaryAuditTimeWindow(t *testing.T) {
	h, token := newBoundaryEnv(t)
	_, _ = post(t, h.ts, "/api/v1/kbs", token, map[string]any{"name": "边界库"}) // 产生一条 kb.create

	// from==to → 排他窗口为空
	sc, body := get(t, h.ts, "/api/v1/admin/audit-logs?from=2026-01-01T00:00:00Z&to=2026-01-01T00:00:00Z", token)
	require.Equal(t, 200, sc, body)
	assert.InDelta(t, 0, body["total"], 0.001)

	// from > to 逆序 → 空结果（不报错）
	sc, body = get(t, h.ts, "/api/v1/admin/audit-logs?from=2027-01-01T00:00:00Z&to=2020-01-01T00:00:00Z", token)
	assert.Equal(t, 200, sc, body)
	assert.InDelta(t, 0, body["total"], 0.001)

	// 非法格式 → 422
	sc, _ = get(t, h.ts, "/api/v1/admin/audit-logs?from=2026/01/01", token)
	assert.Equal(t, 422, sc)

	// 覆盖未来的大窗口 → 全量
	sc, body = get(t, h.ts, "/api/v1/admin/audit-logs?from=2020-01-01T00:00:00Z&to=2099-01-01T00:00:00Z", token)
	require.Equal(t, 200, sc, body)
	assert.GreaterOrEqual(t, body["total"].(float64), float64(1))
}

// ---- 反馈 reason 等价类（1-4 合法；0/5/负数非法） ----

func TestBoundaryFeedbackReason(t *testing.T) {
	h, token, kbID := newChatEnv(t)
	_, body := post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/conversations", token, nil)
	convID := int64(body["id"].(float64))
	sc, _, raw := postSSE(t, h.ts, "/api/v1/conversations/"+i64(convID)+"/messages", token, `{"content":"问题"}`)
	require.Equal(t, 200, sc, raw)
	events := parseSSE(t, raw)
	var done map[string]any
	require.NoError(t, unmarshalLast(t, events, &done))
	msgID := i64(int64(done["message_id"].(float64)))
	path := "/api/v1/messages/" + msgID + "/feedback"

	// 合法类：1 与 4（4 带 comment）
	sc, _ = post(t, h.ts, path, token, map[string]any{"reason": 1})
	assert.Equal(t, 200, sc)
	sc, _ = post(t, h.ts, path, token, map[string]any{"reason": 4, "comment": "说明"})
	assert.Equal(t, 200, sc)

	// 非法类：0、5、负数
	for _, bad := range []int{0, 5, -1} {
		sc, body := post(t, h.ts, path, token, map[string]any{"reason": bad})
		assert.Equal(t, 422, sc, "reason=%d 应拒绝", bad)
		assert.Equal(t, service.CodeValidation, jcode(t, body))
	}
}

// ---- 文档 URL 长度边界（≤2000） ----

func TestBoundaryDocURL(t *testing.T) {
	h, token, kbID := newChatEnv(t)
	path := "/api/v1/kbs/" + i64(kbID) + "/documents/url"

	// 恰 2000 字符合法（201 Created；实现锚定）
	longURL := "https://example.com/" + strings.Repeat("p", 1980)
	sc, body := post(t, h.ts, path, token, map[string]any{"url": longURL})
	assert.Equal(t, 201, sc, body)

	// 2001 越界 → 422
	longURL = "https://example.com/" + strings.Repeat("p", 1981)
	sc, body = post(t, h.ts, path, token, map[string]any{"url": longURL})
	assert.Equal(t, 422, sc)
	assert.Equal(t, service.CodeValidation, jcode(t, body))

	// 空 URL / 非法 URL → 422
	sc, _ = post(t, h.ts, path, token, map[string]any{"url": ""})
	assert.Equal(t, 422, sc)
}

func unmarshalLast(t *testing.T, events []sseEvent, out *map[string]any) error {
	t.Helper()
	require.NotEmpty(t, events)
	return json.Unmarshal([]byte(events[len(events)-1].data), out)
}
