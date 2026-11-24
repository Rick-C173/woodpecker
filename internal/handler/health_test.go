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
	r.GET("/healthz", Health(db, redis))
	r.GET("/readyz", Readyz(db, redis))
	return r
}

func TestHealthOK(t *testing.T) {
	r := testRouter(pingerOK, pingerOK)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)
	assert.Contains(t, w.Body.String(), `"db":"up"`)
	assert.Contains(t, w.Body.String(), `"redis":"up"`)
}

func TestHealthDBDown(t *testing.T) {
	r := testRouter(pingerErr, pingerOK)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"SYS_001"`)
	assert.Contains(t, w.Body.String(), "request_id")
	assert.Contains(t, w.Body.String(), `"db":"down"`)
	assert.Contains(t, w.Body.String(), `"redis":"up"`)
}

// Redis 挂掉（asynq 队列与默认配置缓存同时失效）必须被探针发现（P2-27）。
func TestHealthRedisDown(t *testing.T) {
	r := testRouter(pingerOK, pingerErr)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"redis":"down"`)
	assert.Contains(t, w.Body.String(), `"db":"up"`)
	assert.Contains(t, w.Body.String(), "Redis 不可用")
}

// 未注入 Redis 探针（未配置 Redis 的场景）视为该依赖未参与探测。
func TestHealthRedisProbeAbsent(t *testing.T) {
	r := testRouter(pingerOK, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"redis":"up"`)
}

// /readyz 与 /healthz 复用同一探测逻辑：依赖全就绪 200，任一不可用 503。
func TestReadyz(t *testing.T) {
	ok := testRouter(pingerOK, pingerOK)
	w := httptest.NewRecorder()
	ok.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)

	bad := testRouter(pingerErr, pingerOK)
	w = httptest.NewRecorder()
	bad.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"error"`)
}
