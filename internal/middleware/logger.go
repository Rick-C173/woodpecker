package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger 以 slog 结构化输出每条请求：方法、路径、状态、耗时、request_id。
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		}
		if id, ok := c.Get(RequestIDKey); ok {
			attrs = append(attrs, "request_id", id)
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, "errors", c.Errors.String())
		}

		status := c.Writer.Status()
		switch {
		case status >= 500:
			slog.Error("http", attrs...)
		case status >= 400:
			slog.Warn("http", attrs...)
		default:
			slog.Info("http", attrs...)
		}
	}
}
