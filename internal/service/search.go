package service

import (
	"context"
	"sort"
	"strings"
	"time"

	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// 检索参数（技术选型 §6.2；调参笔记 md/调参笔记.md 记录迭代）。
const (
	searchCandidates  = 50  // 混合检索候选数（送 rerank）
	searchMaxQueryLen = 500 // 查询词最大长度
	searchDefaultPage = 1
	searchDefaultPer  = 20
	searchMaxPer      = 100
)

// SearchStore 混合检索存储（repo.SearchRepo 满足）。
type SearchStore interface {
	HybridSearch(ctx context.Context, kbID int64, queryVec []float32, keyword string, candidates int) ([]repo.SearchHit, error)
}

// SearchConfigResolver 模型配置解析（*ModelConfigService 满足）。
type SearchConfigResolver interface {
	GetDefault(ctx context.Context, purpose int16) (llm.ResolvedConfig, error)
}

// SearchEmbedder 查询向量化（llm.Provider 满足）。
type SearchEmbedder interface {
	Embed(ctx context.Context, cfg llm.ResolvedConfig, texts []string) ([][]float32, error)
}

// SearchReranker 精排（llm.Provider 满足）。
type SearchReranker interface {
	Rerank(ctx context.Context, cfg llm.ResolvedConfig, query string, docs []string) ([]float64, error)
}

// SearchItem 检索结果条目（API设计.md §5 快照字段）。
type SearchItem struct {
	// 分块 ID
	ChunkID int64 `json:"chunk_id"`
	// 所属文档 ID
	DocumentID int64 `json:"document_id"`
	// 文件路径（url 类型为 null）
	FilePath *string `json:"file_path"`
	// 起始行
	LineStart *int `json:"line_start"`
	// 结束行
	LineEnd *int `json:"line_end"`
	// Markdown 标题路径
	HeadingPath *string `json:"heading_path"`
	// 分块内容快照
	Content string `json:"content"`
	// 融合得分（RRF，越小越相关）
	Score float64 `json:"score"`
}

// SearchResult 检索响应（分页信封）。
type SearchResult struct {
	// 命中条目（按相关度排序）
	Items []SearchItem `json:"items"`
	// 命中总数
	Total int64 `json:"total"`
	// 当前页码
	Page int `json:"page"`
	// 每页条数（默认 20，最大 100）
	PageSize int `json:"page_size"`
}

// SearchService 混合检索（FR-10 检索页 + M6 问答引用复用）。
type SearchService struct {
	store    SearchStore
	resolver SearchConfigResolver
	embedder SearchEmbedder
	reranker SearchReranker
	now      func() time.Time
}

func NewSearchService(store SearchStore, resolver SearchConfigResolver, embedder SearchEmbedder, reranker SearchReranker) *SearchService {
	return &SearchService{store: store, resolver: resolver, embedder: embedder, reranker: reranker, now: time.Now}
}

// Search 检索页：查询向量化 → 混合检索（RRF Top-50）→ rerank 精排（缺省配置时降级 RRF 排序）→ 分页。
func (s *SearchService) Search(ctx context.Context, kbID int64, q string, page, perPage int) (*SearchResult, error) {
	items, err := s.searchAll(ctx, kbID, q)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = searchDefaultPage
	}
	if perPage < 1 || perPage > searchMaxPer {
		perPage = searchDefaultPer
	}
	total := int64(len(items))
	start := (page - 1) * perPage
	if start >= len(items) {
		return &SearchResult{Items: []SearchItem{}, Total: total, Page: page, PageSize: perPage}, nil
	}
	end := start + perPage
	if end > len(items) {
		end = len(items)
	}
	return &SearchResult{Items: items[start:end], Total: total, Page: page, PageSize: perPage}, nil
}

// SearchTopN 问答引用（M6）：rerank 精排后取 Top-N（PRD R5 引用上限 5）。
func (s *SearchService) SearchTopN(ctx context.Context, kbID int64, q string, n int) ([]SearchItem, error) {
	if n <= 0 {
		n = 5
	}
	items, err := s.searchAll(ctx, kbID, q)
	if err != nil {
		return nil, err
	}
	if len(items) > n {
		items = items[:n]
	}
	return items, nil
}

// searchAll 公共管道：校验 → 向量化 → 混合检索 → rerank 精排。
func (s *SearchService) searchAll(ctx context.Context, kbID int64, q string) ([]SearchItem, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, NewServiceError(CodeValidation, "查询词 q 不能为空")
	}
	if len([]rune(q)) > searchMaxQueryLen {
		return nil, NewServiceError(CodeValidation, "查询词不超过 500 字符")
	}

	// 查询向量化（默认 embedding 条目；缺省 → MODEL_001）
	cfg, err := s.resolver.GetDefault(ctx, model.ModelPurposeEmbedding)
	if err != nil {
		return nil, err
	}
	vecs, err := s.embedder.Embed(ctx, cfg, []string{q})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, NewServiceError("INTERNAL", "查询向量化无结果")
	}

	hits, err := s.store.HybridSearch(ctx, kbID, vecs[0], q, searchCandidates)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return []SearchItem{}, nil
	}

	// rerank 精排：无默认 rerank 条目或调用失败时降级为 RRF 排序（调参笔记 §2）
	hits = s.tryRerank(ctx, q, hits)

	items := make([]SearchItem, 0, len(hits))
	for _, h := range hits {
		items = append(items, SearchItem{
			ChunkID:     h.ChunkID,
			DocumentID:  h.DocumentID,
			FilePath:    h.FilePath,
			LineStart:   h.LineStart,
			LineEnd:     h.LineEnd,
			HeadingPath: h.HeadingPath,
			Content:     h.Content,
			Score:       h.Score,
		})
	}
	return items, nil
}

// tryRerank 精排重排；rerank 未配置/失败时静默降级（不影响检索可用性）。
func (s *SearchService) tryRerank(ctx context.Context, q string, hits []repo.SearchHit) []repo.SearchHit {
	if s.reranker == nil {
		return hits
	}
	cfg, err := s.resolver.GetDefault(ctx, model.ModelPurposeRerank)
	if err != nil {
		return hits // 未配置默认 rerank：降级
	}
	docs := make([]string, len(hits))
	for i, h := range hits {
		docs[i] = h.Content
	}
	scores, err := s.reranker.Rerank(ctx, cfg, q, docs)
	if err != nil || len(scores) != len(hits) {
		return hits // 精排失败：降级
	}
	// 稳定排序：按 rerank 分数降序，同分保持 RRF 顺序（hits 与 scores 打包后排序）
	type scored struct {
		hit   repo.SearchHit
		score float64
	}
	pairs := make([]scored, len(hits))
	for i, h := range hits {
		pairs[i] = scored{hit: h, score: scores[i]}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })
	for i, p := range pairs {
		hits[i] = p.hit
	}
	return hits
}
