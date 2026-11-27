package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/jwt"
)

// M15（FR-20）双凭证：JWT 服务交互式会话；个人 API Token（wpk_ 前缀）服务机器客户端
// （脚本/CI/CLI，MCP Server 前置）。管理页随前端整体重构落地（方向待办-3）。
const (
	CtxUserID  = "user_id"
	CtxIsAdmin = "is_admin"
	// CtxMustChangePassword 当前会话是否被标记「首次登录必须修改初始密码」。
	CtxMustChangePassword = "must_change_password"
)

// UserState Auth 中间件所需的用户状态快照。
// status/token_version/must_change_password/is_admin 由**一次**查库取全，
// 避免每个请求额外多一次查询（审计 P1-4 要求复用 userLookup 的既有回查）。
type UserState struct {
	Status             int16
	TokenVersion       int
	MustChangePassword bool
	IsAdmin            bool // API Token 路径的 CtxIsAdmin 来源（JWT 路径仍取 claims）
}

// UserLookup 按 user_id 取用户状态；找不到/出错视为登录状态失效。
type UserLookup func(c *gin.Context, userID int64) (UserState, error)

// TokenLookup 校验 API Token 明文（wpk_ 前缀），返回所属用户 ID；
// 无效/吊销/过期由实现返回 *service.ServiceError（TOK_002）。
type TokenLookup func(c *gin.Context, bearer string) (int64, error)

// Auth 双凭证鉴权（API 设计 §1）：
//   - Bearer 串带 wpk_ 前缀 → API Token 路径：token 校验（TOK_002）→ 用户状态回查
//     （禁用即 AUTH_003）；跳过 token_version 比对——改密/权限变更不使机器凭证失效，
//     用户禁用仍即时失效（每请求查 Status）；CtxMustChangePassword 固定 false（机器凭证
//     不受首登改密约束）；CtxIsAdmin 取 DB 实时值
//   - 否则走 JWT：解析失败/过期/版本号不符 → 401 AUTH_004
//
// 注：JWT 路径不逐请求回查 is_admin——权限变更由 service 侧 bump token_version 生效
// （审计 P1-2），一次查询即可同时覆盖「禁用/改密/权限变更」三种失效场景。
func Auth(issuer *jwt.Issuer, lookup UserLookup, tokens TokenLookup) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || tokenStr == "" {
			httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeAuthStaleToken, "未登录或凭证无效")
			return
		}
		if strings.HasPrefix(tokenStr, httpx.TokenPrefix) && tokens != nil {
			userID, err := tokens(c, tokenStr)
			if err != nil {
				httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeTokenInvalid, "API Token 无效或已吊销/过期")
				return
			}
			state, err := lookup(c, userID)
			if err != nil {
				httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeTokenInvalid, "API Token 无效")
				return
			}
			if state.Status != 1 {
				httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeAuthDisabled, "账号已被禁用，请联系管理员")
				return
			}
			c.Set(CtxUserID, userID)
			c.Set(CtxIsAdmin, state.IsAdmin)
			c.Set(CtxMustChangePassword, false)
			c.Next()
			return
		}
		claims, err := issuer.Parse(tokenStr)
		if err != nil {
			httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeAuthStaleToken, "登录状态已失效，请重新登录")
			return
		}
		state, err := lookup(c, claims.UserID)
		if err != nil {
			httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeAuthStaleToken, "登录状态已失效，请重新登录")
			return
		}
		if state.Status != 1 {
			httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeAuthDisabled, "账号已被禁用，请联系管理员")
			return
		}
		if state.TokenVersion != claims.TokenVersion {
			httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeAuthStaleToken, "登录状态已失效，请重新登录")
			return
		}
		c.Set(CtxUserID, claims.UserID)
		c.Set(CtxIsAdmin, claims.IsAdmin)
		c.Set(CtxMustChangePassword, state.MustChangePassword)
		c.Next()
	}
}

// passwordChangeAllowlist 强制改密期间仍可访问的端点（"METHOD 完整路径"）。
//
// 改密接口的正式路径是 POST /api/v1/auth/password（API 设计 §3.1）；
// /auth/change-password、/auth/logout、/auth/me 为契约预留路径（当前路由未实现），
// 一并列入白名单，后续新增这些路由时无需再改中间件。
var passwordChangeAllowlist = map[string]struct{}{
	"POST /api/v1/auth/password":        {},
	"POST /api/v1/auth/change-password": {},
	"POST /api/v1/auth/logout":          {},
	"GET /api/v1/auth/me":               {},
	"GET /api/v1/healthz":               {},
	"GET /healthz":                      {},
}

// RequirePasswordChanged 「首次登录强制修改初始密码」的服务端强制（PRD FR-01 / 审计 P1-4）。
// 必须挂在 Auth 之后；标记为 true 的会话只放行 passwordChangeAllowlist，其余一律 403 AUTH_07。
// 改密成功后 service 把 must_change_password 置 false 并 bump token_version（service/auth.go
// ChangePassword），下一次请求从 DB 读到的最新值即为 false，放行自然恢复。
func RequirePasswordChanged() gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get(CtxMustChangePassword)
		if !ok || v != true {
			c.Next()
			return
		}
		if _, allowed := passwordChangeAllowlist[c.Request.Method+" "+c.Request.URL.Path]; allowed {
			c.Next()
			return
		}
		httpx.AbortFailWith(c, http.StatusForbidden, httpx.CodeAuthMustChangePassword, "需先修改初始密码")
	}
}

// Admin 管理员门槛（/admin/*），须挂在 Auth 之后。
func Admin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if adm, _ := c.Get(CtxIsAdmin); adm != true {
			httpx.AbortFailWith(c, http.StatusForbidden, httpx.CodeForbidden, "无权限执行该操作")
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

// IsAdmin 当前登录用户是否管理员。
func IsAdmin(c *gin.Context) bool {
	v, ok := c.Get(CtxIsAdmin)
	return ok && v == true
}
