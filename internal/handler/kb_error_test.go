package handler_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKBHandlerErrorPaths 存储层异常经 handler 统一渲染为 500 INTERNAL。
func TestKBHandlerErrorPaths(t *testing.T) {
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)
	_, body = post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "异常库"})
	kbID := int64(body["id"].(float64))
	_, body = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "dev", "password": "dev-pass-123"})
	devID := int64(body["id"].(float64))
	base := "/api/v1/kbs/" + i64(kbID)
	boom := errors.New("boom")

	// List / Get / Update / Delete
	h.env.KBs.ErrOnList = boom
	sc, body := get(t, h.ts, "/api/v1/kbs", rootToken)
	assert.Equal(t, 500, sc)
	assert.Equal(t, "INTERNAL", jcode(t, body))
	h.env.KBs.ErrOnList = nil

	h.env.KBs.ErrOnDocCount = boom
	sc, body = get(t, h.ts, base, rootToken)
	assert.Equal(t, 500, sc)
	assert.Equal(t, "INTERNAL", jcode(t, body))
	h.env.KBs.ErrOnDocCount = nil

	h.env.KBs.ErrOnSave = boom
	sc, body = patch(t, h.ts, base, rootToken, map[string]any{"description": "x"})
	assert.Equal(t, 500, sc)
	assert.Equal(t, "INTERNAL", jcode(t, body))
	h.env.KBs.ErrOnSave = nil

	h.env.KBs.ErrOnDelete = boom
	sc, body = del(t, h.ts, base, rootToken)
	assert.Equal(t, 500, sc)
	assert.Equal(t, "INTERNAL", jcode(t, body))
	h.env.KBs.ErrOnDelete = nil

	// 成员端点
	sc, body = post(t, h.ts, base+"/members", rootToken, map[string]any{})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))
	sc, body = patch(t, h.ts, base+"/members/"+i64(devID), rootToken, map[string]any{})
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	sc, body = post(t, h.ts, base+"/members", rootToken, map[string]any{"user_id": devID, "role": "member"})
	require.Equal(t, 201, sc, body)

	h.env.KBs.ErrOnMembers = boom
	sc, body = get(t, h.ts, base+"/members", rootToken)
	assert.Equal(t, 500, sc)
	assert.Equal(t, "INTERNAL", jcode(t, body))
	sc, _ = post(t, h.ts, base+"/members", rootToken, map[string]any{"user_id": devID, "role": "editor"})
	assert.Equal(t, 500, sc)
	sc, _ = patch(t, h.ts, base+"/members/"+i64(devID), rootToken, map[string]any{"role": "owner"})
	assert.Equal(t, 500, sc)
	sc, _ = del(t, h.ts, base+"/members/"+i64(devID), rootToken)
	assert.Equal(t, 500, sc)
	assert.Equal(t, "INTERNAL", jcode(t, body))
}
