package handler_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/testutil"
)

func docSetupHandler(t *testing.T) (*hEnv, *testutil.FakeDocRepo, *testutil.FakeQueue) {
	t.Helper()
	h := setup(t)
	return h, h.docDocs, h.docQueue
}

func TestDocAPIFlows(t *testing.T) {
	h, docs, queue := docSetupHandler(t)

	// bootstrap 管理员（owner）
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)
	require.NotEmpty(t, rootToken)

	// 建库
	sc, body := post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "文档库"})
	require.Equal(t, 201, sc, body)
	kbID := int64(body["id"].(float64))

	// 编辑者与成员
	_, _ = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "editor", "password": "editor-pass-1"})
	editorToken := loginReadyToken(t, h.ts, "editor", "editor-pass-1", "editor-pass-2")
	_, _ = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "member", "password": "member-pass-1"})
	memberToken := loginReadyToken(t, h.ts, "member", "member-pass-1", "member-pass-2")
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members", rootToken, map[string]any{"user_id": 2, "role": "editor"})
	require.Equal(t, 201, sc)
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/members", rootToken, map[string]any{"user_id": 3, "role": "member"})
	require.Equal(t, 201, sc)

	// 列表（成员可读）
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents", memberToken)
	assert.Equal(t, 200, sc)
	assert.InDelta(t, 0, body["total"], 0.001)

	// URL 接入（编辑者+）
	sc, body = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/url", editorToken, map[string]any{"url": "https://docs.example.com"})
	require.Equal(t, 201, sc, body)
	docID := int64(body["id"].(float64))
	assert.Equal(t, "pending", body["status"])
	assert.Len(t, queue.IDs, 1)

	// Git 接入（编辑者+；凭证不回显）
	sc, body = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/git", editorToken,
		map[string]any{"repo_url": "https://github.com/x/y.git", "git_ref": "main", "credential": "tok-1"})
	require.Equal(t, 201, sc, body)
	gitID := int64(body["id"].(float64))
	assert.NotContains(t, jsonOf(body), "tok-1")

	// 上传（编辑者+；不支持格式逐个报 DOC_002）
	sc, body = postMultipart(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/upload", editorToken,
		[]string{"readme.md", "photo.png"}, [][]byte{[]byte("# hi"), []byte("png")})
	require.Equal(t, 201, sc, body)
	items, _ := body["items"].([]any)
	assert.Len(t, items, 1)
	rejected, _ := body["rejected"].([]any)
	require.Len(t, rejected, 1)
	assert.Equal(t, "DOC_002", rejected[0].(map[string]any)["code"])

	// 校验失败：非法 URL / 空 repo_url
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/url", editorToken, map[string]any{"url": "ftp://x"})
	assert.Equal(t, 422, sc)
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/git", editorToken, map[string]any{})
	assert.Equal(t, 422, sc)
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/git", editorToken, "not-json")
	assert.Equal(t, 422, sc)

	// 详情（成员可读）
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/"+i64(docID), memberToken)
	assert.Equal(t, 200, sc)
	assert.InDelta(t, float64(docID), body["id"], 0.001)

	// 不存在 / 跨库 → 404
	sc, _ = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/9999", memberToken)
	assert.Equal(t, 404, sc)
	sc, _ = get(t, h.ts, "/api/v1/kbs/abc/documents/"+i64(docID), rootToken)
	assert.Equal(t, 404, sc)

	// 状态流转（pending → ready 模拟）
	docs.FinishTasks(gitID) // 模拟接入任务已被 worker 消费
	readyDoc := docs.DocByID(gitID)
	readyDoc.Status = model.DocStatusReady
	require.NoError(t, docs.SaveDocument(t.Context(), readyDoc))

	// 重试（仅失败态）→ 422；同步（git 才行）→ 202；reindex（管理者+）
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/"+i64(gitID)+"/retry", editorToken, nil)
	assert.Equal(t, 422, sc)
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/"+i64(gitID)+"/sync", editorToken, nil)
	assert.Equal(t, 202, sc)
	docs.FinishTasks(gitID) // 模拟同步任务已消费
	// sync 后文档复位为 pending：模拟 worker 完成 → ready
	syncedDoc := docs.DocByID(gitID)
	syncedDoc.Status = model.DocStatusReady
	require.NoError(t, docs.SaveDocument(t.Context(), syncedDoc))
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/"+i64(gitID)+"/reindex", editorToken, nil)
	assert.Equal(t, 403, sc, "reindex 需要管理者（R6）")
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/"+i64(gitID)+"/reindex", rootToken, nil)
	assert.Equal(t, 202, sc)

	// 成员（只读）→ 403
	sc, _ = post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/url", memberToken, map[string]any{"url": "https://x.com"})
	assert.Equal(t, 403, sc)
	// 非成员（不可见）→ 404
	_, _ = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "outsider", "password": "outside-pass-1"})
	outToken := loginReadyToken(t, h.ts, "outsider", "outside-pass-1", "outside-pass-2")
	sc, _ = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents", outToken)
	assert.Equal(t, 404, sc)

	// 删除（管理者+）
	sc, _ = del(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/"+i64(docID), editorToken)
	assert.Equal(t, 403, sc, "删除需要管理者（R6）")
	sc, _ = del(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/"+i64(docID), rootToken)
	assert.Equal(t, 200, sc)
	sc, _ = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/"+i64(docID), rootToken)
	assert.Equal(t, 404, sc)
}

func TestDocUploadMultipartValidation(t *testing.T) {
	h, _, _ := docSetupHandler(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)
	_, kbBody := post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "上传库"})
	kbID := int64(kbBody["id"].(float64))

	// 缺 files[] → 422
	sc, _ := post(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/documents/upload", rootToken, nil)
	assert.Equal(t, 422, sc)
}
