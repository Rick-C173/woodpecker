package vector

import (
	"context"
	"time"
)

// CodeChunk 代码块结构
type CodeChunk struct {
	ID         string    `json:"id"`
	RepoOwner  string    `json:"repo_owner"`
	RepoName   string    `json:"repo_name"`
	FilePath   string    `json:"file_path"`
	Language   string    `json:"language"`
	ChunkType  string    `json:"chunk_type"` // function, class, struct, file, comment
	StartLine  int       `json:"start_line"`
	EndLine    int       `json:"end_line"`
	SymbolName string    `json:"symbol_name,omitempty"`
	Content    string    `json:"content"`
	IndexedAt  time.Time `json:"indexed_at"`
}

// SearchResult 搜索结果
type SearchResult struct {
	Chunk *CodeChunk `json:"chunk"`
	Score float64    `json:"score"`
}

// Filter 搜索过滤器
type Filter struct {
	RepoOwner  string
	RepoName   string
	FilePath   string
	Language   string
	ChunkTypes []string
	Limit      int
	Offset     int
}

// VectorStore 向量存储接口
type VectorStore interface {
	// Initialize 初始化向量存储（创建表/Collection 等）
	Initialize(ctx context.Context) error

	// Upsert 插入或更新向量
	Upsert(ctx context.Context, chunk *CodeChunk, vector []float32) error

	// UpsertBatch 批量插入或更新向量
	UpsertBatch(ctx context.Context, chunks []*CodeChunk, vectors [][]float32) error

	// Search 搜索最相似的向量
	Search(ctx context.Context, queryVector []float32, filter *Filter) ([]*SearchResult, error)

	// Delete 删除向量
	Delete(ctx context.Context, ids []string) error

	// DeleteByFilter 根据过滤器删除
	DeleteByFilter(ctx context.Context, filter *Filter) error

	// Count 统计向量数量
	Count(ctx context.Context, filter *Filter) (int64, error)

	// Close 关闭连接
	Close(ctx context.Context) error
}
