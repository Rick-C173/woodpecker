package repo

import (
	"context"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"

	"woodpecker/internal/model"
)

// SearchHit 混合检索单条命中（快照字段，API设计.md §5）。
type SearchHit struct {
	ChunkID     int64
	DocumentID  int64
	FilePath    *string
	LineStart   *int
	LineEnd     *int
	HeadingPath *string
	Content     string
	Score       float64 // RRF 融合分（k=60）
}

// SearchRepo 手写检索 SQL（数据库设计.md §4：不走 GORM 条件构造）。
type SearchRepo struct{ db *gorm.DB }

func NewSearchRepo(db *gorm.DB) *SearchRepo { return &SearchRepo{db: db} }

// hybridSearchSQL 混合检索：向量 HNSW + trigram 模糊，RRF 融合（k=60），Top-50 送 rerank。
// 两个候选侧均带阈值过滤（见 md/调参笔记.md §1）：
//   - 向量侧余弦距离 > 0.85（相似度 < 0.15）的命中丢弃：HNSW top-N 语义下总会返回最近邻，
//     无阈值则任何查询都有结果，空态（无命中）永远不会出现；
//   - 关键词侧 similarity > 0.05 而非 % 运算符默认 0.3：中文短词（2 字）的 trigram
//     similarity 上限低，默认阈值会导致关键词兜底几乎不命中。
const hybridSearchSQL = `
WITH vec AS (
    SELECT id, row_number() OVER (ORDER BY embedding <=> @vec) AS rank
    FROM chunks
    WHERE kb_id = @kb AND embedding <=> @vec < 0.85
    ORDER BY embedding <=> @vec
    LIMIT @candidate
), kw AS (
    SELECT id, row_number() OVER (ORDER BY similarity(content, @keyword) DESC) AS rank
    FROM chunks
    WHERE kb_id = @kb AND similarity(content, @keyword) > 0.05
    ORDER BY similarity(content, @keyword) DESC
    LIMIT @candidate
)
SELECT c.id AS chunk_id, c.document_id, c.file_path, c.line_start, c.line_end,
       c.heading_path, c.content,
       COALESCE(1.0 / (60 + vec.rank), 0) + COALESCE(1.0 / (60 + kw.rank), 0) AS score
FROM chunks c
LEFT JOIN vec ON vec.id = c.id
LEFT JOIN kw  ON kw.id  = c.id
JOIN documents d ON d.id = c.document_id
WHERE vec.id IS NOT NULL OR kw.id IS NOT NULL
ORDER BY score DESC
LIMIT @candidate`

// HybridSearch 执行混合检索，返回按 RRF 分数降序的候选集（≤50 条）。
// queryVec 为查询向量；keyword 为原始查询词（trigram 模糊兜底）。
func (r *SearchRepo) HybridSearch(ctx context.Context, kbID int64, queryVec []float32, keyword string, candidates int) ([]SearchHit, error) {
	if candidates <= 0 || candidates > 100 {
		candidates = 50
	}
	var rows []SearchHit
	err := r.db.WithContext(ctx).Raw(hybridSearchSQL, map[string]any{
		"kb":        kbID,
		"vec":       pgvector.NewVector(queryVec),
		"keyword":   keyword,
		"candidate": candidates,
	}).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// CountReadyChunks 库内已就绪分块数（空态判定/监控用）。
func (r *SearchRepo) CountReadyChunks(ctx context.Context, kbID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Chunk{}).Where("kb_id = ?", kbID).Count(&n).Error
	return n, err
}
