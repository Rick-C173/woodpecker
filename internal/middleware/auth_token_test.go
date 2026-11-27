package middleware

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/jwt"
	"woodpecker/internal/model"
)

// M15 / FR-20：API Token 双凭证分流测试。
func TestAuthTokenPathRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	issuer := jwt.NewIssuer("test-secret", time.Hour)

	lookup := func(_ *gin.Context, id int64) (UserState, error) {
		states := map[int64]UserState{
			1: {Status: model.UserStatusActive, TokenVersion: 7, IsAdmin: false},
			2: {Status: model.UserStatusActive, TokenVersion: 7, IsAdmin: true},
			3: {Status: model.UserStatusDisabled, TokenVersion: 7},
		}
		st, ok := states[id]
		if !ok {
			return UserState{}, errors.New("nf")
		}
		return st, nil
	}

	newRouter := func(tokens TokenLookup) *gin.Engine {
		r := gin.New()
		r.GET("/p", Auth(issuer, lookup, tokens), func(c *gin.Context) {
			mcp, _ := c.Get(CtxMustChangePassword)
			c.JSON(200, gin.H{"uid": UserID(c), "adm": IsAdmin(c), "mcp": mcp})
		})
		return r
	}

	validTokens := func(_ *gin.Context, bearer string) (int64, error) {
		if bearer == "wpk_good" {
			return 1, nil
		}
		if bearer == "wpk_admin" {
			return 2, nil
		}
		return 0, errors.New("invalid")
	}
	call := func(r *gin.Engine, tk string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/p", nil)
		if tk != "" {
			req.Header.Set("Authorization", "Bearer "+tk)
		}
		r.ServeHTTP(w, req)
		return w
	}

	// 1. tokens=nil 时 wpk_ 串回落 JWT 路径 → 解析失败 AUTH_004
	r := newRouter(nil)
	w := call(r, "wpk_good")
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_004")

	// 2. 有效 token → uid 注入；is_admin 取 DB 实时值（lookup），非 claims
	r = newRouter(validTokens)
	w = call(r, "wpk_good")
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"uid":1,"adm":false,"mcp":false}`, w.Body.String())

	// 3. 管理员 token：CtxIsAdmin 来自 state.IsAdmin
	w = call(r, "wpk_admin")
	require.Equal(t, 200, w.Code)
	assert.JSONEq(t, `{"uid":2,"adm":true,"mcp":false}`, w.Body.String())

	// 4. 无效/吊销 token → TOK_002
	w = call(r, "wpk_bad")
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "TOK_002")

	// 5. token 属于被禁用用户 → AUTH_003（禁用即时失效）
	disabledTokens := func(_ *gin.Context, _ string) (int64, error) { return 3, nil }
	r = newRouter(disabledTokens)
	w = call(r, "wpk_disabled")
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_003")

	// 6. JWT 路径不受影响（带第三参后回归；换回有效 tokens 的路由）
	r = newRouter(validTokens)
	jwtTok, err := issuer.Issue(&model.User{ID: 1, TokenVersion: 7})
	require.NoError(t, err)
	w = call(r, jwtTok)
	require.Equal(t, 200, w.Code, "body=%s", w.Body.String())
	assert.JSONEq(t, `{"uid":1,"adm":false,"mcp":false}`, w.Body.String())

	// 7. 无 header → AUTH_004
	w = call(r, "")
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_004")
}
