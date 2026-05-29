package qa

import (
	"context"
	"fmt"

	"woodpecker/config"
	"woodpecker/internal/vector"
)

// VectorRetriever 基于向量存储的检索器
type VectorRetriever struct {
	vectorStore vector.VectorStore
	embedder    vector.Embedder
	cfg         config.VectorConfig
}

func NewVectorRetriever(
	vectorStore vector.VectorStore,
	embedder vector.Embedder,
	cfg config.VectorConfig,
) *VectorRetriever {
	return &VectorRetriever{
		vectorStore: vectorStore,
		embedder:    embedder,
		cfg:         cfg,
	}
}

func (r *VectorRetriever) Retrieve(
	ctx context.Context,
	query string,
	repoOwner, repoName string,
) ([]*vector.SearchResult, error) {
	// 将查询转换为向量
	queryVector, err := r.embedder.EmbedOne(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// 构建过滤器
	filter := &vector.Filter{
		RepoOwner: repoOwner,
		RepoName:  repoName,
		Limit:     r.cfg.MaxResults,
	}

	if filter.Limit <= 0 {
		filter.Limit = 5
	}

	// 搜索相关代码块
	results, err := r.vectorStore.Search(ctx, queryVector, filter)
	if err != nil {
		return nil, fmt.Errorf("search vector store: %w", err)
	}

	// 过滤低相似度结果
	if r.cfg.ScoreThreshold > 0 {
		filtered := make([]*vector.SearchResult, 0, len(results))
		for _, res := range results {
			if res.Score >= r.cfg.ScoreThreshold {
				filtered = append(filtered, res)
			}
		}
		results = filtered
	}

	return results, nil
}
