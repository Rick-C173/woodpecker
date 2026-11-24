package handler_test

// 时间格式契约回归（审计 P1-17）：API 设计 §1 要求时间字段为 RFC3339 UTC，
// 修复前业务响应统一是本地时区裸字符串 "2006-01-02 15:04:05"（无时区、部分前端解析失败）。

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimeFieldsAreRFC3339UTC 业务响应时间字段必须是 RFC3339 UTC。
func TestTimeFieldsAreRFC3339UTC(t *testing.T) {
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)

	sc, body := post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "时间格式库"})
	require.Equal(t, 201, sc, body)
	kbID := int64(body["id"].(float64))
	for _, field := range []string{"created_at", "updated_at"} {
		raw, _ := body[field].(string)
		require.NotEmpty(t, raw, "%s 不应为空", field)
		ts, err := time.Parse(time.RFC3339, raw)
		require.NoError(t, err, "%s=%q 必须可被 RFC3339 解析", field, raw)
		assert.Equal(t, time.UTC, ts.Location(), "%s 必须为 UTC", field)
	}

	// 文档详情里的任务时间同样走 RFC3339（DocumentTaskItem.created_at）
	sc, body = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/url", rootToken,
		map[string]any{"url": "https://example.com/doc"})
	require.Equal(t, 201, sc, body)
	docID := int64(body["id"].(float64))
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/"+i64(docID), rootToken)
	require.Equal(t, 200, sc, body)
	tasks, _ := body["tasks"].([]any)
	require.NotEmpty(t, tasks)
	first, _ := tasks[0].(map[string]any)
	taskCreated, _ := first["created_at"].(string)
	ts, err := time.Parse(time.RFC3339, taskCreated)
	require.NoError(t, err, "task.created_at=%q 必须可被 RFC3339 解析", taskCreated)
	assert.Equal(t, time.UTC, ts.Location())
}
