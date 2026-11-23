package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
)

// ---- 检索测试替身 ----

type fakeSearchStore struct {
	hits  []repo.SearchHit
	err   error
	gotKB int64
	gotKW string
}

func (f *fakeSearchStore) HybridSearch(_ context.Context, kbID int64, _ []float32, keyword string, _ int) ([]repo.SearchHit, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.gotKB, f.gotKW = kbID, keyword
	return f.hits, nil
}

type fakeSearchEmbedder struct {
	err   error
	empty bool
}

func (f *fakeSearchEmbedder) Embed(context.Context, llm.ResolvedConfig, []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.empty {
		return nil, nil
	}
	return [][]float32{{0.1, 0.2}}, nil
}

type fakeSearchResolver struct {
	embedErr  error
	rerankErr error
}

func (f *fakeSearchResolver) GetDefault(_ context.Context, purpose int16) (llm.ResolvedConfig, error) {
	switch purpose {
	case model.ModelPurposeEmbedding:
		if f.embedErr != nil {
			return llm.ResolvedConfig{}, f.embedErr
		}
		return llm.ResolvedConfig{ModelName: "bge-m3"}, nil
	case model.ModelPurposeRerank:
		if f.rerankErr != nil {
			return llm.ResolvedConfig{}, f.rerankErr
		}
		return llm.ResolvedConfig{ModelName: "bge-reranker"}, nil
	}
	return llm.ResolvedConfig{}, errors.New("unexpected purpose")
}

type fakeSearchReranker struct {
	scores []float64
	err    error
	calls  int
}

func (f *fakeSearchReranker) Rerank(context.Context, llm.ResolvedConfig, string, []string) ([]float64, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.scores, nil
}

func mkHit(id int64, content string) repo.SearchHit {
	return repo.SearchHit{ChunkID: id, DocumentID: 100 + id, Content: content, Score: 1.0 / (60 + float64(id))}
}

func newSearchSvc(store service.SearchStore, embed *fakeSearchEmbedder, resolver *fakeSearchResolver, rerank *fakeSearchReranker) *service.SearchService {
	return service.NewSearchService(store, resolver, embed, rerank)
}

// ---- 用例 ----

func TestSearchNormalAndPaging(t *testing.T) {
	store := &fakeSearchStore{hits: []repo.SearchHit{mkHit(1, "限流实现"), mkHit(2, "限流配置"), mkHit(3, "无关")}}
	svc := newSearchSvc(store, &fakeSearchEmbedder{}, &fakeSearchResolver{}, &fakeSearchReranker{})
	ctx := context.Background()

	res, err := svc.Search(ctx, 10, "限流实现", 1, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), res.Total)
	require.Len(t, res.Items, 2)
	assert.Equal(t, int64(1), res.Items[0].ChunkID)
	assert.Equal(t, int64(10), store.gotKB)
	assert.Equal(t, "限流实现", store.gotKW)

	// 第二页：只剩 1 条
	res, err = svc.Search(ctx, 10, "限流实现", 2, 2)
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	assert.Equal(t, int64(3), res.Items[0].ChunkID)

	// 页越界 → 空但 total 正确
	res, err = svc.Search(ctx, 10, "限流实现", 9, 2)
	require.NoError(t, err)
	assert.Empty(t, res.Items)
	assert.Equal(t, int64(3), res.Total)

	// page_size 非法 → 归一化 20
	res, err = svc.Search(ctx, 10, "限流实现", 0, 999)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Page)
	assert.Equal(t, 20, res.PageSize)
}

func TestSearchValidation(t *testing.T) {
	svc := newSearchSvc(&fakeSearchStore{}, &fakeSearchEmbedder{}, &fakeSearchResolver{}, nil)

	// 空查询 → 422
	_, err := svc.Search(context.Background(), 10, "  ", 1, 20)
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	// 超长查询 → 422
	long := make([]rune, 501)
	for i := range long {
		long[i] = '词'
	}
	_, err = svc.Search(context.Background(), 10, string(long), 1, 20)
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
}

func TestSearchEmptyState(t *testing.T) {
	store := &fakeSearchStore{hits: []repo.SearchHit{}}
	svc := newSearchSvc(store, &fakeSearchEmbedder{}, &fakeSearchResolver{}, nil)
	res, err := svc.Search(context.Background(), 10, "不存在的词", 1, 20)
	require.NoError(t, err)
	assert.Empty(t, res.Items, "空态：items 为空数组")
	assert.Equal(t, int64(0), res.Total)
}

func TestSearchEmbeddingConfigMissing(t *testing.T) {
	svc := newSearchSvc(&fakeSearchStore{}, &fakeSearchEmbedder{}, &fakeSearchResolver{embedErr: service.NewServiceError("MODEL_001", "缺默认")}, nil)
	_, err := svc.Search(context.Background(), 10, "q", 1, 20)
	assert.Equal(t, "MODEL_001", svcErr(t, err).Code, "缺默认 embedding 透传 MODEL_001")
}

func TestSearchEmbedEmptyResult(t *testing.T) {
	svc := newSearchSvc(&fakeSearchStore{}, &fakeSearchEmbedder{empty: true}, &fakeSearchResolver{}, nil)
	_, err := svc.Search(context.Background(), 10, "q", 1, 20)
	assert.Equal(t, "INTERNAL", svcErr(t, err).Code)
}

func TestSearchEmbedErrorPassthrough(t *testing.T) {
	boom := errors.New("模型服务不可用")
	svc := newSearchSvc(&fakeSearchStore{}, &fakeSearchEmbedder{err: boom}, &fakeSearchResolver{}, nil)
	_, err := svc.Search(context.Background(), 10, "q", 1, 20)
	require.ErrorIs(t, err, boom)
}

func TestSearchStoreErrorPassthrough(t *testing.T) {
	boom := errors.New("db down")
	svc := newSearchSvc(&fakeSearchStore{err: boom}, &fakeSearchEmbedder{}, &fakeSearchResolver{}, nil)
	_, err := svc.Search(context.Background(), 10, "q", 1, 20)
	require.ErrorIs(t, err, boom)
}

func TestSearchRerankReorder(t *testing.T) {
	// RRF 顺序 1,2,3；rerank 希望顺序 3,1,2
	store := &fakeSearchStore{hits: []repo.SearchHit{mkHit(1, "a"), mkHit(2, "b"), mkHit(3, "c")}}
	rerank := &fakeSearchReranker{scores: []float64{0.5, 0.9, 0.99}}
	svc := newSearchSvc(store, &fakeSearchEmbedder{}, &fakeSearchResolver{}, rerank)

	res, err := svc.Search(context.Background(), 10, "q", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 1, rerank.calls)
	assert.Equal(t, int64(3), res.Items[0].ChunkID, "rerank 分数最高的排最前")
	assert.Equal(t, int64(2), res.Items[1].ChunkID)
	assert.Equal(t, int64(1), res.Items[2].ChunkID)
	// score 字段保留 RRF 融合分（API 示例量级）
	assert.Less(t, res.Items[0].Score, 0.05)
}

func TestSearchRerankNotConfiguredFallsBack(t *testing.T) {
	store := &fakeSearchStore{hits: []repo.SearchHit{mkHit(1, "a"), mkHit(2, "b")}}
	rerank := &fakeSearchReranker{scores: []float64{0.9, 0.1}}
	svc := newSearchSvc(store, &fakeSearchEmbedder{}, &fakeSearchResolver{rerankErr: service.NewServiceError("MODEL_001", "缺默认")}, rerank)

	res, err := svc.Search(context.Background(), 10, "q", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, 0, rerank.calls, "未配置 rerank 时不调用")
	assert.Equal(t, int64(1), res.Items[0].ChunkID, "保持 RRF 顺序")
}

func TestSearchRerankErrorFallsBack(t *testing.T) {
	store := &fakeSearchStore{hits: []repo.SearchHit{mkHit(1, "a"), mkHit(2, "b")}}
	rerank := &fakeSearchReranker{err: errors.New("rerank down")}
	svc := newSearchSvc(store, &fakeSearchEmbedder{}, &fakeSearchResolver{}, rerank)

	res, err := svc.Search(context.Background(), 10, "q", 1, 20)
	require.NoError(t, err, "rerank 失败应降级而非报错")
	assert.Equal(t, int64(1), res.Items[0].ChunkID, "保持 RRF 顺序")
}

func TestSearchTopN(t *testing.T) {
	hits := []repo.SearchHit{mkHit(1, "a"), mkHit(2, "b"), mkHit(3, "c"), mkHit(4, "d"), mkHit(5, "e"), mkHit(6, "f")}
	store := &fakeSearchStore{hits: hits}
	rerank := &fakeSearchReranker{scores: []float64{0.1, 0.2, 0.3, 0.4, 0.9, 0.5}}
	svc := newSearchSvc(store, &fakeSearchEmbedder{}, &fakeSearchResolver{}, rerank)

	items, err := svc.SearchTopN(context.Background(), 10, "q", 5)
	require.NoError(t, err)
	require.Len(t, items, 5, "R5：引用上限 Top-5")
	assert.Equal(t, int64(5), items[0].ChunkID, "rerank 最高分 0.9")
	assert.Equal(t, int64(6), items[1].ChunkID)
}
