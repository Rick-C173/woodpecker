package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRateLimiterWindow(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewRateLimiter(2, time.Minute)
	l.SetClock(func() time.Time { return now })

	ok, _ := l.Allow("u:1")
	assert.True(t, ok)
	ok, _ = l.Allow("u:1")
	assert.True(t, ok)
	ok, retry := l.Allow("u:1")
	assert.False(t, ok)
	assert.Greater(t, retry, time.Duration(0))

	// 不同 key 互不影响
	ok, _ = l.Allow("u:2")
	assert.True(t, ok)

	// 窗口滚动后恢复
	now = now.Add(time.Minute)
	ok, _ = l.Allow("u:1")
	assert.True(t, ok)
}

func TestRateLimiterUnlimited(t *testing.T) {
	for _, limit := range []int{0, -1} {
		l := NewRateLimiter(limit, time.Minute)
		for i := 0; i < 10; i++ {
			ok, _ := l.Allow("k")
			assert.True(t, ok)
		}
	}
}

func TestRateLimiterBucketSweep(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewRateLimiter(1, time.Minute)
	l.SetClock(func() time.Time { return now })
	for i := 0; i < 5000; i++ {
		l.Allow("k" + strconv.Itoa(i))
	}
	now = now.Add(2 * time.Minute)
	ok, _ := l.Allow("fresh")
	assert.True(t, ok)
}

func TestRateLimitHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	l := NewRateLimiter(1, time.Minute)
	r := gin.New()
	r.Use(l.Handler(func(*gin.Context) string { return "k" }))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, 200, w.Code)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, 429, w.Code)
	assert.Contains(t, w.Body.String(), "RATE_LIMITED")
	assert.NotEmpty(t, w.Header().Get("Retry-After"))
}

func TestUserKey(t *testing.T) {
	c := testCtx()
	c.Set(CtxUserID, int64(42))
	assert.Equal(t, "u:42", UserKey(c))

	c2 := testCtx()
	assert.Contains(t, UserKey(c2), "ip:")
}
