package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func newCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequestWithContext(context.Background(), "GET", "/x", nil)
	c.Set("request_id", "rid-1")
	return c, w
}

func TestResponseHelpers(t *testing.T) {
	c, w := newCtx()
	OK(c, gin.H{"a": 1})
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"a":1`)

	c, w = newCtx()
	Created(c, gin.H{"b": 1})
	assert.Equal(t, 201, w.Code)

	c, w = newCtx()
	Accepted(c, gin.H{"c": 1})
	assert.Equal(t, 202, w.Code)

	c, w = newCtx()
	PageOK(c, []int{1, 2}, 7, 2, 20)
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"total":7`)
	assert.Contains(t, w.Body.String(), `"page":2`)

	c, w = newCtx()
	Fail(c, CodeAuthLocked, "已锁定")
	assert.Equal(t, 401, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"AUTH_002"`)
	assert.Contains(t, w.Body.String(), `"request_id":"rid-1"`)

	c, w = newCtx()
	FailWith(c, http.StatusNotFound, CodeNotFound, "不存在")
	assert.Equal(t, 404, w.Code)
}

func TestStatusOf(t *testing.T) {
	assert.Equal(t, 401, StatusOf(CodeAuthInvalid))
	assert.Equal(t, 409, StatusOf(CodeUserExists))
	assert.Equal(t, 429, StatusOf(CodeRateLimited))
	assert.Equal(t, 503, StatusOf(CodeDBUnreachable))
	assert.Equal(t, 500, StatusOf("UNKNOWN_CODE"), "未知码兜底 500")
}

func TestFailServiceError(t *testing.T) {
	c, w := newCtx()
	FailServiceError(c, NewServiceError(CodeUserExists, "重名"))
	assert.Equal(t, 409, w.Code)
	assert.Contains(t, w.Body.String(), "USER_001")

	c, w = newCtx()
	FailServiceError(c, errors.New("raw error"))
	assert.Equal(t, 500, w.Code)
	assert.Contains(t, w.Body.String(), "INTERNAL")
}

func TestAbortHelpers(t *testing.T) {
	c, w := newCtx()
	AbortFail(c, CodeNotFound, "不存在")
	assert.Equal(t, 404, w.Code)
	assert.True(t, c.IsAborted())

	c, w = newCtx()
	AbortFailWith(c, http.StatusTooManyRequests, CodeRateLimited, "限流")
	assert.Equal(t, 429, w.Code)
	assert.True(t, c.IsAborted())
}
