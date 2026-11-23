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

func testRouter(pinger Pinger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/healthz", Health(pinger))
	return r
}

func TestHealthOK(t *testing.T) {
	r := testRouter(func(_ context.Context) error { return nil })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"ok"`)
}

func TestHealthDBDown(t *testing.T) {
	r := testRouter(func(_ context.Context) error { return errors.New("boom") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"SYS_001"`)
	assert.Contains(t, w.Body.String(), "request_id")
}
