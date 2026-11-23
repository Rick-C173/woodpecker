package middleware

import (
	"context"
	"errors"
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
	}
	lookup := func(_ *gin.Context, id int64) (int16, int, error) {
		u, ok := users[id]
		if !ok {
			return 0, 0, errors.New("nf")
		}
		return u.Status, u.TokenVersion, nil
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
	r := gin.New()
	r.Use(RequestID(), Logger())
	r.GET("/boom", func(c *gin.Context) {
		c.Error(errors.New("handler boom")) // 触发日志的 errors 字段分支
		c.Status(500)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/boom", nil))
	assert.Equal(t, 500, w.Code)
}

func TestUserIDAbsent(t *testing.T) {
	c := testCtx()
	assert.Zero(t, UserID(c))
	_ = context.Background()
}
