package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
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
	ts           *httptest.Server
	env          *testutil.TestEnv
	rootToken    string
	mcTester     *testutil.FakeTester
	mcRepo       *testutil.FakeModelCfgRepo
	docDocs      *testutil.FakeDocRepo
	docQueue     *testutil.FakeQueue
	docSvc       *service.DocumentService
	searchStore  *fakeSearchHandlerStore
	chatStore    *fakeChatStore
	chatStream   *fakeChatStreamH
	chatSearch   *fakeChatSearchH
	feedbackRepo *testutil.FakeFeedbackRepo
}

func setup(t *testing.T) *hEnv {
	t.Helper()
	env := testutil.NewTestEnv(t, nil)
	cfg := &config.Config{LogLevel: "debug", CORSOrigins: "*", JWTTLHours: 24}
	mcRepo := testutil.NewFakeModelCfgRepo()
	mcTester := &testutil.FakeTester{}
	mcSvc := service.NewModelConfigService(mcRepo, &testutil.FakeCipher{}, testutil.NewFakeCache(), mcTester, env.AuditSvc)
	// M4：文档 handler（fake 存储与队列）
	docDocs := testutil.NewFakeDocs()
	docQueue := &testutil.FakeQueue{}
	docSvc := service.NewDocumentService(docDocs, env.Settings, &testutil.FakeCipher{}, docQueue, env.AuditSvc)
	docSvc.SetUploadDir(t.TempDir())
	// M5：检索 handler（fake 存储与配置/向量化）
	searchStore := &fakeSearchHandlerStore{}
	searchSvc := service.NewSearchService(searchStore, &fakeSearchResolverHandler{}, &fakeSearchEmbedderHandler{}, nil)
	// M6：会话问答 handler（fake 存储与流式器）
	chatStore := newFakeChatStore()
	chatStream := &fakeChatStreamH{deltas: []string{"回答内容 [1]"}} // 带 [1] 才会产生引用（审计 P1-16）
	chatSearch := &fakeChatSearchH{}
	chatSvc := service.NewChatService(chatStore, env.KBs, &fakeChatResolverH{}, chatStream, chatSearch)
	// M7：系统设置与审计/反馈查询 handler（fake 查询仓储）
	feedbackRepo := &testutil.FakeFeedbackRepo{}
	settingsSvc := service.NewSettingsService(env.Settings, env.AuditSvc)
	adminQuerySvc := service.NewAdminQueryService(env.Audit, feedbackRepo)
	router := handler.NewRouter(handler.Deps{
		Cfg:           cfg,
		Auth:          handler.NewAuthHandler(env.AuthSvc),
		Admin:         handler.NewAdminUsersHandler(env.AdminSvc),
		KB:            handler.NewKBHandler(env.KBSvc),
		ModelCfg:      handler.NewModelCfgHandler(mcSvc),
		Document:      handler.NewDocumentHandler(docSvc),
		Search:        handler.NewSearchHandler(searchSvc),
		Chat:          handler.NewChatHandler(chatSvc),
		AdminSettings: handler.NewAdminSettingsHandler(settingsSvc, adminQuerySvc),
		UserRepo:      env.Users,
		Issuer:        env.Issuer,
		KBLookup:      env.KBs,
		OnAdminKBAccess: func(ctx context.Context, userID, kbID int64, _, _, _ string) {
			_ = env.Audit.Add(ctx, &model.AuditLog{
				UserID: &userID, Action: "kb.admin_access", ObjectType: "kb", ObjectID: &kbID,
			})
		},
		Pinger: func(_ context.Context) error { return nil },
	})
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)
	return &hEnv{ts: ts, env: env, mcTester: mcTester, mcRepo: mcRepo, docDocs: docDocs, docQueue: docQueue, docSvc: docSvc, searchStore: searchStore, chatStore: chatStore, chatStream: chatStream, chatSearch: chatSearch, feedbackRepo: feedbackRepo}
}

// postMultipart 提交 multipart/form-data（files[] 字段）。
func postMultipart(t *testing.T, ts *httptest.Server, path, token string, names []string, contents [][]byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for i, name := range names {
		fw, _ := w.CreateFormFile("files[]", name)
		_, _ = fw.Write(contents[i])
	}
	_ = w.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+path, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
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
	req, _ := http.NewRequestWithContext(context.Background(), method, ts.URL+path, buf)
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
	// /healthz 是 liveness 探针（审查 #3 拆分）：仅进程存活，不带依赖明细
	sc, body := get(t, h.ts, "/healthz", "")
	assert.Equal(t, 200, sc)
	assert.Equal(t, "ok", body["status"])
	assert.Nil(t, body["db"])
	assert.Nil(t, body["redis"])
	sc, _ = get(t, h.ts, "/api/v1/healthz", "")
	assert.Equal(t, 200, sc)
	// /readyz 是 readiness 探针（P2-27）：依赖就绪判定，带 db/redis 明细
	sc, body = get(t, h.ts, "/readyz", "")
	assert.Equal(t, 200, sc)
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "up", body["db"])
	assert.Equal(t, "up", body["redis"])
	sc, _ = get(t, h.ts, "/api/v1/readyz", "")
	assert.Equal(t, 200, sc)
}

// 上传前置限制：文件数超 50 在读文件之前就拒掉（P2-28）。
func TestUploadRejectsTooManyFiles(t *testing.T) {
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)
	require.NotEmpty(t, rootToken)
	sc, body := post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "上传限制库"})
	require.Equal(t, 201, sc, body)
	kbID := int64(body["id"].(float64))

	names := make([]string, 51) // 超 R2 的 50 个上限
	contents := make([][]byte, 51)
	for i := range names {
		names[i] = "f.md"
		contents[i] = []byte("# hi")
	}
	sc, body = postMultipart(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/upload", rootToken, names, contents)
	assert.Equal(t, 422, sc, body)
	assert.Equal(t, "DOC_001", jcode(t, body))
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
	assert.Equal(t, true, body["must_change_password"], "管理员创建的账号首登必须改密")
	devToken, _ := body["token"].(string)

	// P1-4：首登未改密 → 业务接口 403 AUTH_007（服务端强制，不依赖前端）
	sc, body = get(t, h.ts, "/api/v1/kbs", devToken)
	assert.Equal(t, 403, sc)
	assert.Equal(t, "AUTH_007", jcode(t, body))

	sc, body = get(t, h.ts, "/api/v1/admin/users", devToken)
	assert.Equal(t, 403, sc)
	assert.Equal(t, "FORBIDDEN", jcode(t, body))

	// 用户列表（管理员）→ 分页信封
	sc, body = get(t, h.ts, "/api/v1/admin/users?q=dev&status=1&page=1&page_size=10", rootToken)
	assert.Equal(t, 200, sc)
	assert.InDelta(t, 1, body["total"], 0.001)

	// 强制改密 → 改密成功
	sc, body = post(t, h.ts, "/api/v1/auth/password", devToken, map[string]any{"old_password": initial, "new_password": "dev01-new-pass"})
	assert.Equal(t, 200, sc, body)
	newToken, _ := body["token"].(string)

	// P1-4：改密后标记清除，业务接口恢复可用
	sc, body = get(t, h.ts, "/api/v1/kbs", newToken)
	assert.Equal(t, 200, sc, body)

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

// TestAuthBindFailures 注册与改密的请求体绑定失败分支（边界：非法/缺字段）。
func TestAuthBindFailures(t *testing.T) {
	h := setup(t)

	// 注册：非 JSON body → 422 VALIDATION_FAILED（请求校验；登录失败才是 AUTH_001）
	sc, body := post(t, h.ts, "/api/v1/auth/register", "", "not-json")
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))
	// 缺密码字段
	sc, body = post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "x"})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	// 改密：非 JSON / 缺新密码 → 422
	_, _ = post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	token := loginToken(t, h.ts, "root", "password8")
	sc, body = post(t, h.ts, "/api/v1/auth/password", token, "not-json")
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))
	sc, _ = post(t, h.ts, "/api/v1/auth/password", token, map[string]any{"old_password": "password8"})
	assert.Equal(t, 422, sc)
}

// TestAdminListPaginationBounds 管理员列表分页参数归一化（边界值）。
func TestAdminListPaginationBounds(t *testing.T) {
	h := setup(t)
	_, _ = post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	token := loginToken(t, h.ts, "root", "password8")

	// page=0 / page_size 超上限 101 → 归一化 page=1、page_size=20
	sc, body := get(t, h.ts, "/api/v1/admin/users?page=0&page_size=101", token)
	assert.Equal(t, 200, sc)
	assert.InDelta(t, 1, body["page"], 0.001)
	assert.InDelta(t, 20, body["page_size"], 0.001)

	// 非数字 / 负数 → 归一化
	sc, body = get(t, h.ts, "/api/v1/admin/users?page=abc&page_size=xyz", token)
	assert.Equal(t, 200, sc)
	assert.InDelta(t, 1, body["page"], 0.001)
	assert.InDelta(t, 20, body["page_size"], 0.001)
	sc, _ = get(t, h.ts, "/api/v1/admin/users?page=-5&page_size=0", token)
	assert.Equal(t, 200, sc)

	// 合法上限：page_size=100 透传
	sc, body = get(t, h.ts, "/api/v1/admin/users?page=2&page_size=100", token)
	assert.Equal(t, 200, sc)
	assert.InDelta(t, 2, body["page"], 0.001)
	assert.InDelta(t, 100, body["page_size"], 0.001)

	// status 非法值不报错（0=不过滤）
	sc, _ = get(t, h.ts, "/api/v1/admin/users?status=abc", token)
	assert.Equal(t, 200, sc)
}

// TestAdminBindFailures 创建用户与重置密码的绑定失败分支。
func TestAdminBindFailures(t *testing.T) {
	h := setup(t)
	_, _ = post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	token := loginToken(t, h.ts, "root", "password8")

	// 创建用户：非 JSON / 缺用户名 → 422 请求校验
	sc, body := post(t, h.ts, "/api/v1/admin/users", token, "not-json")
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))
	sc, _ = post(t, h.ts, "/api/v1/admin/users", token, map[string]any{})
	assert.Equal(t, 422, sc)

	// 重置密码：目标用户不存在 → 404
	sc, _ = post(t, h.ts, "/api/v1/admin/users/9999/reset-password", token, nil)
	assert.Equal(t, 404, sc)
}
