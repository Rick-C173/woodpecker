package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 上传请求体超过硬上限 → 413，且在 multipart 解析阶段就被 MaxBytesReader 截断（P2-28）。
func TestUploadRejectsOversizedBody(t *testing.T) {
	old := uploadBodyLimit
	uploadBodyLimit = 1024
	t.Cleanup(func() { uploadBodyLimit = old })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/upload", NewDocumentHandler(nil).CreateUpload)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("files[]", "big.md")
	require.NoError(t, err)
	_, err = fw.Write(bytes.Repeat([]byte("x"), 8<<10))
	require.NoError(t, err)
	assert.NoError(t, w.Close())

	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Contains(t, rec.Body.String(), "DOC_001")
}
