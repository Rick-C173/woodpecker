//go:build integration

// M1 集成测试：验收标准 —— AUTH_001~004、USER_001~003 全部可复现，
// 禁用账号下一个请求即 401。环境准备见 setup_test.go。
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/cache"
	"woodpecker/internal/config"
	"woodpecker/internal/crypto"
	"woodpecker/internal/handler"
	"woodpecker/internal/jwt"
	"woodpecker/internal/llm"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
)

type env struct {
	ts     *httptest.Server
	issuer *jwt.Issuer
	db     *gormDB
}

func setup(t *testing.T) *env {
	t.Helper()
	db := openTestDB(t)
	truncateAll(db)

	cfg := &config.Config{LogLevel: "debug", JWTSecret: "it-secret", JWTTLHours: 24, CORSOrigins: "*"}
	users := repo.NewUserRepo(db)
	auditSvc := service.NewAuditService(repo.NewAuditRepo(db))
	settings := repo.NewSettingRepo(db)
	issuer := jwt.NewIssuer(cfg.JWTSecret, time.Hour)
	authSvc := service.NewAuthService(users, settings, auditSvc, issuer)
	adminSvc := service.NewUserAdminService(users, auditSvc)
	kbs := repo.NewKBRepo(db)
	kbSvc := service.NewKBService(kbs, auditSvc)
	// M3：模型配置（AES-GCM 用独立测试密钥；缓存用 Noop 保持环境封闭）
	cipher, err := crypto.NewAESGCM("it-encryption-key")
	require.NoError(t, err)
	modelCfgRepo := repo.NewModelCfgRepo(db)
	modelCfgSvc := service.NewModelConfigService(modelCfgRepo, cipher, cache.NoopCache{}, llm.NewOpenAIProvider(), auditSvc)

	router := handler.NewRouter(handler.Deps{
		Cfg:      cfg,
		DB:       db,
		Auth:     handler.NewAuthHandler(authSvc),
		Admin:    handler.NewAdminUsersHandler(adminSvc),
		KB:       handler.NewKBHandler(kbSvc),
		ModelCfg: handler.NewModelCfgHandler(modelCfgSvc),
		UserRepo: users,
		Issuer:   issuer,
		KBLookup: kbs,
		OnAdminKBAccess: func(ctx context.Context, userID, kbID int64, method, path, ip string) {
			auditSvc.Record(ctx, &userID, "kb.admin_access", "kb", &kbID,
				map[string]any{"method": method, "path": path}, ip)
		},
	})
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)
	return &env{ts: ts, issuer: issuer, db: db}
}

// call 极简 JSON 客户端，返回状态码与解析后的 body。
func (e *env) call(t *testing.T, method, path, token string, payload any) (int, map[string]any) {
	t.Helper()
	buf := bytes.NewBuffer(nil)
	if payload != nil {
		b, _ := json.Marshal(payload)
		buf = bytes.NewBuffer(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, buf)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w, err := e.ts.Client().Do(req)
	require.NoError(t, err)
	defer w.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(w.Body).Decode(&out)
	return w.StatusCode, out
}

func code(t *testing.T, body map[string]any) string {
	t.Helper()
	c, _ := body["code"].(string)
	return c
}

func i64(v int64) string { return strconv.FormatInt(v, 10) }

func (e *env) count(t *testing.T, sql string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, e.db.Raw(sql).Scan(&n).Error)
	return n
}

func TestM1Acceptance(t *testing.T) {
	e := setup(t)

	// 1. 首个注册用户自动成为管理员（bootstrap 不受注册开关限制）
	sc, sb := e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	require.Equal(t, http.StatusCreated, sc, sb)
	assert.Equal(t, true, sb["is_admin"], sb)
	rootToken, _ := sb["token"].(string)
	rootID := int64(sb["user_id"].(float64))

	// 2. 之后注册关闭 → AUTH_005
	sc, sb = e.call(t, "POST", "/api/v1/auth/register", "", map[string]any{"username": "x", "password": "password8"})
	assert.Equal(t, http.StatusForbidden, sc)
	assert.Equal(t, "AUTH_005", code(t, sb))

	// 3. 管理员创建用户 → 一次性初始密码 + 强制改密
	sc, sb = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "alice"})
	require.Equal(t, http.StatusCreated, sc, sb)
	initialPw, _ := sb["initial_password"].(string)
	require.NotEmpty(t, initialPw)
	aliceID := int64(sb["id"].(float64))

	// 4. USER_001 重名
	sc, sb = e.call(t, "POST", "/api/v1/admin/users", rootToken, map[string]any{"username": "alice"})
	assert.Equal(t, http.StatusConflict, sc)
	assert.Equal(t, "USER_001", code(t, sb))

	// 5. AUTH_001 密码错误
	sc, sb = e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "wrong-pass"})
	assert.Equal(t, http.StatusUnauthorized, sc)
	assert.Equal(t, "AUTH_001", code(t, sb))

	// 6. 首登强制改密标记
	sc, sb = e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": initialPw})
	require.Equal(t, http.StatusOK, sc, sb)
	assert.Equal(t, true, sb["must_change_password"], sb)
	aliceToken1, _ := sb["token"].(string)

	// 7. 改密 → 旧 token 失效（AUTH_004）
	sc, sb = e.call(t, "POST", "/api/v1/auth/password", aliceToken1, map[string]any{"old_password": initialPw, "new_password": "newpass99"})
	require.Equal(t, http.StatusOK, sc, sb)
	aliceToken2, _ := sb["token"].(string)
	sc, sb = e.call(t, "GET", "/api/v1/admin/users", aliceToken1, nil)
	assert.Equal(t, http.StatusUnauthorized, sc)
	assert.Equal(t, "AUTH_004", code(t, sb))
	// 旧密码再登录 → AUTH_001
	sc, sb = e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": initialPw})
	assert.Equal(t, "AUTH_001", code(t, sb))

	// 8. AUTH_006 弱密码
	sc, sb = e.call(t, "POST", "/api/v1/auth/password", aliceToken2, map[string]any{"old_password": "newpass99", "new_password": "short"})
	assert.Equal(t, http.StatusUnprocessableEntity, sc)
	assert.Equal(t, "AUTH_006", code(t, sb))

	// 9. 非管理员访问管理端点 → 403
	sc, sb = e.call(t, "GET", "/api/v1/admin/users", aliceToken2, nil)
	assert.Equal(t, http.StatusForbidden, sc)
	assert.Equal(t, "FORBIDDEN", code(t, sb))

	// 10. 禁用 alice → 下一个请求 401 AUTH_003
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/users/"+i64(aliceID), rootToken, map[string]any{"status": 2})
	require.Equal(t, http.StatusOK, sc, sb)
	sc, sb = e.call(t, "GET", "/api/v1/admin/users", aliceToken2, nil)
	assert.Equal(t, http.StatusUnauthorized, sc)
	assert.Equal(t, "AUTH_003", code(t, sb))

	// 11. USER_002 不能禁用自己
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/users/"+i64(rootID), rootToken, map[string]any{"status": 2})
	assert.Equal(t, http.StatusUnprocessableEntity, sc)
	assert.Equal(t, "USER_002", code(t, sb))

	// 12. USER_003 最后一个管理员不可降级
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/users/"+i64(rootID), rootToken, map[string]any{"is_admin": false})
	assert.Equal(t, http.StatusUnprocessableEntity, sc)
	assert.Equal(t, "USER_003", code(t, sb))

	// 13. 锁定：先重新启用 alice（步骤 10 已禁用），再连错 5 次 → AUTH_002
	sc, sb = e.call(t, "PATCH", "/api/v1/admin/users/"+i64(aliceID), rootToken, map[string]any{"status": 1})
	require.Equal(t, http.StatusOK, sc, sb)
	for i := 0; i < 5; i++ {
		_, _ = e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "wrong-pass"})
	}
	sc, sb = e.call(t, "POST", "/api/v1/auth/login", "", map[string]any{"username": "alice", "password": "newpass99"})
	assert.Equal(t, http.StatusUnauthorized, sc)
	assert.Equal(t, "AUTH_002", code(t, sb))

	// 14. 审计已记录（FR-15 写入侧）
	n := e.count(t, "SELECT count(*) FROM audit_logs WHERE action IN ('auth.login_failed','auth.locked','user.create','user.disable')")
	assert.Greater(t, n, int64(0), "应已有审计记录")
}
