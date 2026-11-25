package middleware

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/httpx"
	"woodpecker/internal/jwt"
	"woodpecker/internal/model"
)

func testCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	return c
}

func TestRequestIDGenerateAndPassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID())
	var got string
	r.GET("/x", func(c *gin.Context) {
		if v, ok := c.Get(RequestIDKey); ok {
			got, _ = v.(string)
		}
		c.Status(200)
	})

	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest("GET", "/x", nil))
	assert.NotEmpty(t, got)
	assert.NotEmpty(t, w1.Header().Get("X-Request-ID"))

	// 透传外部 request_id
	w2 := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Request-ID", "my-trace-id")
	r.ServeHTTP(w2, req)
	assert.Equal(t, "my-trace-id", w2.Header().Get("X-Request-ID"))
	assert.Equal(t, "my-trace-id", got)
}

func TestLogger(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestID(), Logger())
	r.GET("/ok", func(c *gin.Context) { c.Status(200) })
	r.GET("/err", func(c *gin.Context) { c.Status(500) })

	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, httptest.NewRequest("GET", "/ok", nil))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest("GET", "/err", nil))
	assert.Equal(t, 500, w2.Code)
}

func TestCORS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS("http://a.com, http://b.com"))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	// 允许的来源回显
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Origin", "http://a.com")
	r.ServeHTTP(w, req)
	assert.Equal(t, "http://a.com", w.Header().Get("Access-Control-Allow-Origin"))

	// 预检
	w = httptest.NewRecorder()
	req = httptest.NewRequest("OPTIONS", "/x", nil)
	req.Header.Set("Origin", "http://a.com")
	r.ServeHTTP(w, req)
	assert.Equal(t, 204, w.Code)

	// 未允许来源：不带 CORS 头
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Origin", "http://evil.com")
	r.ServeHTTP(w, req)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	issuer := jwt.NewIssuer("it-secret", time.Hour)

	users := map[int64]*model.User{
		1: {ID: 1, Status: model.UserStatusActive, TokenVersion: 2},
		2: {ID: 2, Status: model.UserStatusDisabled, TokenVersion: 0},
		3: {ID: 3, Status: model.UserStatusActive, TokenVersion: 0, MustChangePassword: true},
	}
	lookup := func(_ *gin.Context, id int64) (UserState, error) {
		u, ok := users[id]
		if !ok {
			return UserState{}, errors.New("nf")
		}
		return UserState{Status: u.Status, TokenVersion: u.TokenVersion, MustChangePassword: u.MustChangePassword}, nil
	}

	r := gin.New()
	r.GET("/p", Auth(issuer, lookup), func(c *gin.Context) {
		adm, _ := c.Get(CtxIsAdmin)
		c.JSON(200, gin.H{"uid": UserID(c), "adm": adm})
	})

	// 无 header → 401 AUTH_004
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/p", nil))
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_004")

	// 坏 token → 401
	w = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", "Bearer garbage")
	r.ServeHTTP(w, req)
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_004")

	// 非法 Authorization 头（非 Bearer）→ 401
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", "Basic abc")
	r.ServeHTTP(w, req)
	assert.Equal(t, 401, w.Code)

	// 有效 token → 通过，claims 注入
	u := &model.User{ID: 1, IsAdmin: true, TokenVersion: 2}
	token, err := issuer.Issue(u)
	require.NoError(t, err)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"uid":1`)
	assert.Contains(t, w.Body.String(), `"adm":true`)

	// 用户不存在（token 有效但查库无）→ 401
	u2 := &model.User{ID: 99, TokenVersion: 0}
	token99, _ := issuer.Issue(u2)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", "Bearer "+token99)
	r.ServeHTTP(w, req)
	assert.Equal(t, 401, w.Code)

	// 禁用用户 → 401 AUTH_003
	u3 := &model.User{ID: 2, TokenVersion: 0}
	token2, _ := issuer.Issue(u3)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", "Bearer "+token2)
	r.ServeHTTP(w, req)
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_003")

	// 版本号不符（改密/禁用后旧 token）→ 401 AUTH_004
	u1old := &model.User{ID: 1, TokenVersion: 1}
	tokenOld, _ := issuer.Issue(u1old)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/p", nil)
	req.Header.Set("Authorization", "Bearer "+tokenOld)
	r.ServeHTTP(w, req)
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_004")
}

// TestRequirePasswordChanged 首登强制改密拦截（审计 P1-4）：
// 标记为 true 的会话只能访问改密白名单，其余一律 403 AUTH_07。
func TestRequirePasswordChanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	issuer := jwt.NewIssuer("it-secret", time.Hour)
	lookup := func(_ *gin.Context, id int64) (UserState, error) {
		// id=3 为「首登未改密」账号
		return UserState{Status: model.UserStatusActive, TokenVersion: 0, MustChangePassword: id == 3}, nil
	}
	token, err := issuer.Issue(&model.User{ID: 3, TokenVersion: 0})
	require.NoError(t, err)
	normal, err := issuer.Issue(&model.User{ID: 1, TokenVersion: 0})
	require.NoError(t, err)

	r := gin.New()
	g := r.Group("/api/v1", Auth(issuer, lookup))
	g.POST("/auth/password", func(c *gin.Context) { c.Status(200) })
	guarded := g.Group("", RequirePasswordChanged())
	guarded.GET("/kbs", func(c *gin.Context) { c.Status(200) })
	guarded.GET("/admin/users", func(c *gin.Context) { c.Status(200) })
	r.GET("/healthz", func(c *gin.Context) { c.Status(200) })

	call := func(method, path, tk string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, nil)
		if tk != "" {
			req.Header.Set("Authorization", "Bearer "+tk)
		}
		r.ServeHTTP(w, req)
		return w
	}

	// 改密白名单：放行
	assert.Equal(t, 200, call("POST", "/api/v1/auth/password", token).Code)
	// 业务接口：403 AUTH_07
	w := call("GET", "/api/v1/kbs", token)
	assert.Equal(t, 403, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_07")

	// 未标记账号：正常放行
	assert.Equal(t, 200, call("GET", "/api/v1/kbs", normal).Code)

	// 版本号不符优先于状态拦截（旧 token 应先被判 401，而非 403）
	stale := func(_ *gin.Context, _ int64) (UserState, error) {
		return UserState{Status: model.UserStatusActive, TokenVersion: 9, MustChangePassword: true}, nil
	}
	r2 := gin.New()
	r2.GET("/api/v1/kbs", Auth(issuer, stale), RequirePasswordChanged(), func(c *gin.Context) { c.Status(200) })
	w = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/kbs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r2.ServeHTTP(w, req)
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_004")
}

func TestAdminMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(CtxIsAdmin, true); c.Next() }, Admin())
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	// 管理员 → 放行
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	assert.Equal(t, 200, w.Code)

	// 非管理员 → 403
	r2 := gin.New()
	r2.Use(func(c *gin.Context) { c.Set(CtxIsAdmin, false); c.Next() }, Admin())
	r2.GET("/x", func(c *gin.Context) { c.Status(200) })
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	assert.Equal(t, 403, w.Code)
	assert.Contains(t, w.Body.String(), httpx.CodeForbidden)
}

func TestLoggerAddsErrorContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 捕获 slog 输出，验证 errors 字段的有无（防止条件变异存活）
	var buf bytes.Buffer
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldDefault) })

	r := gin.New()
	r.Use(RequestID(), Logger())
	r.GET("/boom", func(c *gin.Context) {
		_ = c.Error(errors.New("handler boom")) // 触发日志的 errors 字段分支
		c.Status(500)
	})
	r.GET("/ok", func(c *gin.Context) { c.Status(200) })

	buf.Reset()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/boom", nil))
	assert.Equal(t, 500, w.Code)
	assert.Contains(t, buf.String(), "errors=", "请求带 error 时日志应附 errors 字段")

	buf.Reset()
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/ok", nil))
	assert.Equal(t, 200, w.Code)
	assert.NotContains(t, buf.String(), "errors=", "无 error 请求日志不应含 errors 字段")
}

func TestUserIDAbsent(t *testing.T) {
	c := testCtx()
	assert.Zero(t, UserID(c))
	_ = context.Background()
}
