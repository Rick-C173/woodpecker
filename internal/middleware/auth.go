package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/jwt"
)

const (
	CtxUserID  = "user_id"
	CtxIsAdmin = "is_admin"
)

// Auth JWT 鉴权（API 设计 §1）：
//   - 解析失败 / token 过期 / 版本号不符 → 401 AUTH_004
//   - 用户已被禁用 → 401 AUTH_003（禁用立即生效，PRD FR-13）
func Auth(issuer *jwt.Issuer, lookup func(c *gin.Context, userID int64) (status int16, tokenVersion int, err error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || tokenStr == "" {
			httpx.FailWith(c, http.StatusUnauthorized, httpx.CodeAuthStaleToken, "未登录或凭证无效")
			return
		}
		claims, err := issuer.Parse(tokenStr)
		if err != nil {
			httpx.FailWith(c, http.StatusUnauthorized, httpx.CodeAuthStaleToken, "登录状态已失效，请重新登录")
			return
		}
		status, version, err := lookup(c, claims.UserID)
		if err != nil {
			httpx.FailWith(c, http.StatusUnauthorized, httpx.CodeAuthStaleToken, "登录状态已失效，请重新登录")
			return
		}
		if status != 1 {
			httpx.FailWith(c, http.StatusUnauthorized, httpx.CodeAuthDisabled, "账号已被禁用，请联系管理员")
			return
		}
		if version != claims.TokenVersion {
			httpx.FailWith(c, http.StatusUnauthorized, httpx.CodeAuthStaleToken, "登录状态已失效，请重新登录")
			return
		}
		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxIsAdmin, claims.IsAdmin)
		c.Next()
	}
}

// Admin 管理员门槛（/admin/*），须挂在 Auth 之后。
func Admin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if adm, _ := c.Get(CtxIsAdmin); adm != true {
			httpx.FailWith(c, http.StatusForbidden, httpx.CodeForbidden, "无权限执行该操作")
			return
		}
		c.Next()
	}
}

// UserID 从 ctx 取当前登录用户 ID（handler 层使用）。
func UserID(c *gin.Context) int64 {
	if v, ok := c.Get(CtxUserID); ok {
		if id, ok := v.(int64); ok {
			return id
		}
	}
	return 0
}

