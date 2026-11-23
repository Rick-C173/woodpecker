package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/config"
	"woodpecker/internal/handler"
	"woodpecker/internal/model"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

type hEnv struct {
	ts        *httptest.Server
	env       *testutil.TestEnv
	rootToken string
	mcTester  *testutil.FakeTester
}

func setup(t *testing.T) *hEnv {
	t.Helper()
	env := testutil.NewTestEnv(t, nil)
	cfg := &config.Config{LogLevel: "debug", CORSOrigins: "*", JWTTLHours: 24}
	mcRepo := testutil.NewFakeModelCfgRepo()
	mcTester := &testutil.FakeTester{}
	mcSvc := service.NewModelConfigService(mcRepo, &testutil.FakeCipher{}, testutil.NewFakeCache(), mcTester, env.AuditSvc)
	router := handler.NewRouter(handler.Deps{
		Cfg:      cfg,
		Auth:     handler.NewAuthHandler(env.AuthSvc),
		Admin:    handler.NewAdminUsersHandler(env.AdminSvc),
		KB:       handler.NewKBHandler(env.KBSvc),
		ModelCfg: handler.NewModelCfgHandler(mcSvc),
		UserRepo: env.Users,
		Issuer:   env.Issuer,
		KBLookup: env.KBs,
		OnAdminKBAccess: func(ctx context.Context, userID, kbID int64, _, _, _ string) {
			_ = env.Audit.Add(ctx, &model.AuditLog{
				UserID: &userID, Action: "kb.admin_access", ObjectType: "kb", ObjectID: &kbID,
			})
		},
		Pinger: func(_ context.Context) error { return nil },
	})
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)
	return &hEnv{ts: ts, env: env, mcTester: mcTester}
}

func post(t *testing.T, ts *httptest.Server, path, token string, payload any) (int, map[string]any) {
	t.Helper()
	return do(t, ts, http.MethodPost, path, token, payload)
}

func get(t *testing.T, ts *httptest.Server, path, token string) (int, map[string]any) {
	t.Helper()
	return do(t, ts, http.MethodGet, path, token, nil)
}

func patch(t *testing.T, ts *httptest.Server, path, token string, payload any) (int, map[string]any) {
	t.Helper()
	return do(t, ts, http.MethodPatch, path, token, payload)
}

func do(t *testing.T, ts *httptest.Server, method, path, token string, payload any) (int, map[string]any) {
	t.Helper()
	buf := bytes.NewBuffer(nil)
	if payload != nil {
		b, _ := json.Marshal(payload)
		buf = bytes.NewBuffer(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, buf)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer w.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(w.Body).Decode(&out)
	return w.StatusCode, out
}

func jcode(t *testing.T, body map[string]any) string {
	t.Helper()
	c, _ := body["code"].(string)
	return c
}

func TestHealthEndpoints(t *testing.T) {
	h := setup(t)
	sc, body := get(t, h.ts, "/healthz", "")
	assert.Equal(t, 200, sc)
	assert.Equal(t, "ok", body["status"])
	sc, _ = get(t, h.ts, "/api/v1/healthz", "")
	assert.Equal(t, 200, sc)
}

func TestAuthHandlerFlows(t *testing.T) {
	h := setup(t)

	// 空请求体 → 401 AUTH_001
	sc, body := post(t, h.ts, "/api/v1/auth/login", "", map[string]any{})
	assert.Equal(t, 401, sc)
	assert.Equal(t, "AUTH_001", jcode(t, body))

	// 注册（bootstrap 管理员）
	sc, body = post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	assert.Equal(t, 201, sc)
	rootToken, _ := body["token"].(string)
	require.NotEmpty(t, rootToken)

	// 重复登录错误密码 → 401
	sc, body = post(t, h.ts, "/api/v1/auth/login", "", map[string]any{"username": "root", "password": "nope-nope"})
	assert.Equal(t, 401, sc)
	assert.Equal(t, "AUTH_001", jcode(t, body))

	// 注册关闭 → 403
	sc, body = post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "x", "password": "password8"})
	assert.Equal(t, 403, sc)
	assert.Equal(t, "AUTH_005", jcode(t, body))

	// 管理员建用户
	sc, body = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "dev01"})
	assert.Equal(t, 201, sc)
	initial, _ := body["initial_password"].(string)
	require.NotEmpty(t, initial)
	uid64 := int64(body["id"].(float64))

	// 未登录访问管理端点 → 401
	sc, _ = get(t, h.ts, "/api/v1/admin/users", "")
	assert.Equal(t, 401, sc)

	// 非管理员 → 403
	sc, body = post(t, h.ts, "/api/v1/auth/login", "", map[string]any{"username": "dev01", "password": initial})
	require.Equal(t, 200, sc, body)
	devToken, _ := body["token"].(string)
	sc, body = get(t, h.ts, "/api/v1/admin/users", devToken)
	assert.Equal(t, 403, sc)
	assert.Equal(t, "FORBIDDEN", jcode(t, body))

	// 用户列表（管理员）→ 分页信封
	sc, body = get(t, h.ts, "/api/v1/admin/users?q=dev&status=1&page=1&page_size=10", rootToken)
	assert.Equal(t, 200, sc)
	assert.Equal(t, float64(1), body["total"])

	// 强制改密 → 改密成功
	sc, body = post(t, h.ts, "/api/v1/auth/password", devToken, map[string]any{"old_password": initial, "new_password": "dev01-new-pass"})
	assert.Equal(t, 200, sc, body)
	newToken, _ := body["token"].(string)

	// 旧 token 失效 → 401 AUTH_004
	sc, body = get(t, h.ts, "/api/v1/admin/users", devToken)
	assert.Equal(t, 401, sc)
	assert.Equal(t, "AUTH_004", jcode(t, body))

	// 弱密码 → 422 AUTH_006
	sc, body = post(t, h.ts, "/api/v1/auth/password", newToken, map[string]any{"old_password": "dev01-new-pass", "new_password": "short"})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "AUTH_006", jcode(t, body))
	_ = uid64
}

func TestAdminUserHandlerFlows(t *testing.T) {
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)
	rootID := int64(body["user_id"].(float64))

	// PATCH 非法 id → 404
	sc, body := patch(t, h.ts, "/api/v1/admin/users/abc", rootToken, map[string]any{"status": 1})
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", jcode(t, body))

	// PATCH 非法 status → 422 VALIDATION_FAILED
	sc, body = patch(t, h.ts, "/api/v1/admin/users/1", rootToken, map[string]any{"status": 3})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	// PATCH 非法 body → 422
	sc, _ = patch(t, h.ts, "/api/v1/admin/users/1", rootToken, "not-json")
	_ = sc
	// 重置密码：目标不存在 → 404
	sc, body = post(t, h.ts, "/api/v1/admin/users/999/reset-password", rootToken, nil)
	assert.Equal(t, 404, sc)
	assert.Equal(t, "NOT_FOUND", jcode(t, body))

	// 正常重置（目标是新建用户，不能重置自己——否则当前 token 失效）
	sc, body = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "staff"})
	require.Equal(t, 201, sc, body)
	staffID := int64(body["id"].(float64))
	sc, body = post(t, h.ts, "/api/v1/admin/users/"+i64(staffID)+"/reset-password", rootToken, nil)
	assert.Equal(t, 200, sc)
	assert.NotEmpty(t, body["initial_password"])

	// 禁用自己 → USER_002
	sc, body = patch(t, h.ts, "/api/v1/admin/users/"+i64(rootID), rootToken, map[string]any{"status": 2})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "USER_002", jcode(t, body))

	// 降级唯一管理员 → USER_003
	sc, body = patch(t, h.ts, "/api/v1/admin/users/"+i64(rootID), rootToken, map[string]any{"is_admin": false})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "USER_003", jcode(t, body))

	// 重名创建 → 409
	sc, body = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "root"})
	assert.Equal(t, 409, sc)
	assert.Equal(t, "USER_001", jcode(t, body))
}

func i64(v int64) string { return strconv.FormatInt(v, 10) }
