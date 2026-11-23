package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
)

type rateBucket struct {
	start time.Time
	count int
}

// RateLimiter 固定窗口限流器（API 设计 §1）：全局 100 req/min/用户，问答 10 req/min/用户。
// 进程内实现，单实例部署足够；水平扩容时替换为 Redis 实现（保持 Handler/Allow 语义）。
type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	buckets map[string]*rateBucket
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		buckets: map[string]*rateBucket{},
	}
}

// SetClock 注入时钟（测试用）。
func (l *RateLimiter) SetClock(now func() time.Time) { l.now = now }

// Allow 判定 key 是否放行；不放行时返回需等待的时长。
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	if l.limit <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.buckets) > 4096 { // 防止 key 无限增长
		for k, b := range l.buckets {
			if now.Sub(b.start) >= l.window {
				delete(l.buckets, k)
			}
		}
	}
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		l.buckets[key] = &rateBucket{start: now, count: 1}
		return true, 0
	}
	if b.count >= l.limit {
		return false, l.window - now.Sub(b.start)
	}
	b.count++
	return true, 0
}

// Handler 限流中间件：keyFn 决定限流维度（用户/接口等）。
func (l *RateLimiter) Handler(keyFn func(c *gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, retry := l.Allow(keyFn(c))
		if !ok {
			secs := int(retry/time.Second) + 1
			c.Header("Retry-After", strconv.Itoa(secs))
			httpx.AbortFailWith(c, http.StatusTooManyRequests, httpx.CodeRateLimited, "请求过于频繁，请稍后再试")
			return
		}
		c.Next()
	}
}

// UserKey 按登录用户限流；未登录时退回客户端 IP。
func UserKey(c *gin.Context) string {
	if id := UserID(c); id > 0 {
		return "u:" + strconv.FormatInt(id, 10)
	}
	return "ip:" + c.ClientIP()
}
