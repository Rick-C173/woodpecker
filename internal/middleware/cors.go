package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// CORS 按 CORS_ORIGINS（逗号分隔，* 表示全放行）放行跨域，开发期前后端分离使用。
func CORS(origins string) gin.HandlerFunc {
	allowed := strings.Split(origins, ",")
	for i := range allowed {
		allowed[i] = strings.TrimSpace(allowed[i])
	}
	check := func(origin string) bool {
		for _, a := range allowed {
			if a == "*" || a == origin {
				return true
			}
		}
		return false
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && check(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			c.Header("Access-Control-Max-Age", "86400")
		}
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}
