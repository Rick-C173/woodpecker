package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// Retry-After 精确语义：retry = 窗口剩余时长（Allow）与整秒向上取整（Handler 头）。
func TestRateLimiterRetryAfterValues(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewRateLimiter(2, time.Minute)
	l.SetClock(func() time.Time { return now })

	_, _ = l.Allow("u:1")
	_, _ = l.Allow("u:1")
	// 耗尽后同刻再次请求：剩余时长恰为整个窗口
	ok, retry := l.Allow("u:1")
	assert.False(t, ok)
	assert.Equal(t, time.Minute, retry, "retry 应等于窗口减去已消耗时长")

	// 已消耗 10s 后：retry = 60s - 10s = 50s（加减号/负号变异在 elapsed=0 时等效，必须带消耗量）
	now = now.Add(10 * time.Second)
	ok, retry = l.Allow("u:1")
	assert.False(t, ok)
	assert.Equal(t, 50*time.Second, retry)

	// Handler 头：整秒向上取整（剩余 50s → 51）
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(l.Handler(func(*gin.Context) string { return "u:1" }))
	r.GET("/x", func(c *gin.Context) { c.Status(200) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, 429, w.Code)
	assert.Equal(t, "51", w.Header().Get("Retry-After"))
}

// 桶清理断言：超阈值后仅过期桶被清理，未过期桶保留。
func TestRateLimiterSweepAssert(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewRateLimiter(1, time.Minute)
	l.SetClock(func() time.Time { return now })

	for i := 0; i < 4200; i++ {
		_, _ = l.Allow("old" + strconv.Itoa(i))
	}
	require.Len(t, l.buckets, 4200)

	// 时钟推进：旧桶全部过期；先放一个未过期的 key
	now = now.Add(30 * time.Second)
	_, _ = l.Allow("keep")
	now = now.Add(31 * time.Second) // 越过 60s 窗口，"old*" 全过期，"keep" 仍在窗口内
	_, _ = l.Allow("fresh")         // 触发清理（len > 4096）

	assert.Less(t, len(l.buckets), 4200, "过期桶应被清理")
	assert.Contains(t, l.buckets, "keep", "未过期桶应保留")
	assert.Contains(t, l.buckets, "fresh")
	assert.NotContains(t, l.buckets, "old0")
}

// 清理阈值边界：恰好 4096 个 key 时不触发清理（严格大于）。
func TestRateLimiterSweepBoundaryAt4096(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewRateLimiter(1, time.Minute)
	l.SetClock(func() time.Time { return now })

	for i := 0; i < 4096; i++ {
		_, _ = l.Allow("old" + strconv.Itoa(i))
	}
	now = now.Add(2 * time.Minute) // 全部过期
	_, _ = l.Allow("fresh")
	assert.Len(t, l.buckets, 4097, "恰好 4096 个（未严格大于阈值）不触发清理")
}

// 窗口边界：桶龄恰好等于窗口即视为过期（>=，含边界清理）。
func TestRateLimiterSweepWindowBoundary(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := NewRateLimiter(1, time.Minute)
	l.SetClock(func() time.Time { return now })

	for i := 0; i < 4100; i++ {
		_, _ = l.Allow("old" + strconv.Itoa(i))
	}
	now = now.Add(time.Minute) // 恰好一个窗口
	_, _ = l.Allow("fresh")
	assert.Len(t, l.buckets, 1, "桶龄恰好等于窗口也应清理，仅剩新桶")
}
