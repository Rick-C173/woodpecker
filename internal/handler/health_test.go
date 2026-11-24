package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func pingerOK(_ context.Context) error  { return nil }
func pingerErr(_ context.Context) error { return errors.New("boom") }

func testRouter(db, redis Pinger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/healthz", Health())
	r.GET("/readyz", Readyz(db, redis))
	return r
}

// /healthz 是 liveness 探针：仅进程存活即 200，不探测 DB/Redis（审查 #3：依赖抖动时
// liveness 失败触发容器重启只会放大故障；依赖就绪判定归 /readyz）。
func TestHealthzLivenessOnly(t *testing.T) {
	for name, p := range map[string]Pinger{"ok": pingerOK, "err": pingerErr} {
		r := testRouter(p, p)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

		assert.Equal(t, http.StatusOK, w.Code, name)
		assert.Contains(t, w.Body.String(), `"status":"ok"`, name)
		assert.NotContains(t, w.Body.String(), `"db"`, name)
		assert.NotContains(t, w.Body.String(), `"redis"`, name)
	}
}

// /readyz 是 readiness 探针：依赖全就绪 200（审查 #3 拆分后的依赖判定入口）。
func TestReadyzOK(t *testing.T) {
	r := testRouter(pingerOK, pingerOK)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)
	assert.Contains(t, w.Body.String(), `"db":"up"`)
	assert.Contains(t, w.Body.String(), `"redis":"up"`)
}

// DB 挂掉必须被就绪探针发现（P2-27）：503 + SYS_001 + 依赖明细。
func TestReadyzDBDown(t *testing.T) {
	r := testRouter(pingerErr, pingerOK)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"SYS_001"`)
	assert.Contains(t, w.Body.String(), "request_id")
	assert.Contains(t, w.Body.String(), `"db":"down"`)
	assert.Contains(t, w.Body.String(), `"redis":"up"`)
}

// Redis 挂掉（asynq 队列与默认配置缓存同时失效）必须被就绪探针发现，
// 且错误码为 SYS_002（审查 #2：SYS_001 语义是「数据库不可用」，不能混用）。
func TestReadyzRedisDown(t *testing.T) {
	r := testRouter(pingerOK, pingerErr)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"SYS_002"`)
	assert.Contains(t, w.Body.String(), `"redis":"down"`)
	assert.Contains(t, w.Body.String(), `"db":"up"`)
	assert.Contains(t, w.Body.String(), "Redis 不可用")
}

// 双依赖全挂：503 且消息合并（仍报 SYS_001——以 DB 为主导依赖）。
func TestReadyzBothDown(t *testing.T) {
	r := testRouter(pingerErr, pingerErr)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"SYS_001"`)
	assert.Contains(t, w.Body.String(), "数据库与 Redis 均不可用")
}

// 未注入 Redis 探针（未配置 Redis 的场景）视为该依赖未参与探测。
func TestReadyzRedisProbeAbsent(t *testing.T) {
	r := testRouter(pingerOK, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"redis":"up"`)
}
