package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
)

// MCPAuth Streamable HTTP MCP 端点（/mcp，M17）的专用鉴权：**仅**放行
// 「Bearer + wpk_ 前缀个人 API Token」，JWT 一律 401——机器凭证语义：
// MCP 客户端配置的是长期凭证，JWT 会话过期会导致连接反复断开。
// 校验复用 TokenLookup（apiTokenLookup 闭包），无效/吊销/过期统一 TOK_002。
// 挂载方保证 tokens 非 nil（router.go 中 d.Tokens != nil 才注册 /mcp）。
func MCPAuth(tokens TokenLookup) gin.HandlerFunc {
	return func(c *gin.Context) {
		bearer, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || !strings.HasPrefix(bearer, httpx.TokenPrefix) {
			httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeTokenInvalid,
				"MCP 端点仅接受个人 API Token（Authorization: Bearer wpk_...）")
			return
		}
		if _, err := tokens(c, bearer); err != nil {
			httpx.AbortFailWith(c, http.StatusUnauthorized, httpx.CodeTokenInvalid, "API Token 无效或已吊销/过期")
			return
		}
		c.Next()
	}
}
