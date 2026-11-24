package handler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/llm"
	"woodpecker/internal/repo"
)

// fakeSearchHandlerStore handler 层检索存储替身（setup 注入，测试内改 hits）。
type fakeSearchHandlerStore struct {
	hits []repo.SearchHit
}

func (f *fakeSearchHandlerStore) HybridSearch(context.Context, int64, []float32, string, int) ([]repo.SearchHit, error) {
	return f.hits, nil
}

type fakeSearchResolverHandler struct{}

func (fakeSearchResolverHandler) GetDefault(context.Context, int16) (llm.ResolvedConfig, error) {
	return llm.ResolvedConfig{ModelName: "bge-m3"}, nil
}

type fakeSearchEmbedderHandler struct{}

func (fakeSearchEmbedderHandler) Embed(context.Context, llm.ResolvedConfig, []string) ([][]float32, error) {
	return [][]float32{{0.1}}, nil
}

func intptr(v int) *int        { return &v }
func strPtrH(v string) *string { return &v }

func TestSearchAPIFlows(t *testing.T) {
	h := setup(t)
	h.searchStore.hits = []repo.SearchHit{
		{ChunkID: 31, DocumentID: 12, FilePath: strPtrH("internal/middleware/ratelimit.go"), LineStart: intptr(20), LineEnd: intptr(48), Content: "限流实现", Score: 0.0317},
	}

	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)
	require.NotEmpty(t, rootToken)
	sc, body := post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "检索库"})
	require.Equal(t, 201, sc, body)
	kbID := int64(body["id"].(float64))

	// 正常检索：快照字段齐全
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/search?q=限流实现&page=1", rootToken)
	require.Equal(t, 200, sc, body)
	items, _ := body["items"].([]any)
	require.Len(t, items, 1)
	first, _ := items[0].(map[string]any)
	assert.InDelta(t, float64(31), first["chunk_id"], 0.001)
	assert.Equal(t, "internal/middleware/ratelimit.go", first["file_path"])
	assert.InDelta(t, 0.0317, first["score"], 0.0001)
	assert.Contains(t, body, "total")

	// 空 q → 422
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/search?q=", rootToken)
	assert.Equal(t, 422, sc)
	assert.Equal(t, "VALIDATION_FAILED", jcode(t, body))

	// 缺 q 参数 → 422
	sc, _ = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/search", rootToken)
	assert.Equal(t, 422, sc)

	// 无命中 → 200 空态
	h.searchStore.hits = nil
	sc, body = get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/search?q=别的词", rootToken)
	assert.Equal(t, 200, sc)
	assert.Empty(t, body["items"])
	assert.InDelta(t, 0, body["total"], 0.001)
}

func TestSearchNotMemberInvisible(t *testing.T) {
	h := setup(t)
	_, body := post(t, h.ts, "/api/v1/auth/register", "", map[string]any{"username": "root", "password": "password8"})
	rootToken, _ := body["token"].(string)
	_, kbBody := post(t, h.ts, "/api/v1/kbs", rootToken, map[string]any{"name": "私密库"})
	kbID := int64(kbBody["id"].(float64))

	// 非成员 → 404（KBAccess 不可见原则）
	_, _ = post(t, h.ts, "/api/v1/admin/users", rootToken, map[string]any{"username": "guest", "password": "guest-pass-1"})
	guestToken := loginReadyToken(t, h.ts, "guest", "guest-pass-1", "guest-pass-2")
	sc, _ := get(t, h.ts, "/api/v1/kbs/"+i64(kbID)+"/search?q=x", guestToken)
	assert.Equal(t, 404, sc)
}
