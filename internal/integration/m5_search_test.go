//go:build integration

// M5 集成测试：混合检索 SQL（向量 HNSW + trigram，RRF k=60）真库验证。
// 验收：预设问题返回正确文件与行号；空结果空态正确。
package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/pgvector/pgvector-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// seedChunk 直接插入一条分块（含 1024 维测试向量）。
func seedChunk(t *testing.T, db *gormDB, kbID, docID int64, idx int, content string, filePath string, vec []float32) int64 {
	t.Helper()
	var id int64
	err := db.Raw(`INSERT INTO chunks (document_id, kb_id, content, chunk_index, file_path, line_start, line_end, embedding)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?::vector) RETURNING id`,
		docID, kbID, content, idx, filePath, 10, 20, pgvector.NewVector(vec)).Scan(&id).Error
	require.NoError(t, err)
	require.Greater(t, id, int64(0))
	return id
}

func unitVec(first float64) []float32 {
	v := make([]float32, 1024)
	v[0] = float32(first)
	v[1] = float32(1 - first)
	return v
}

// orthVec 与 unitVec 所张平面正交（余弦距离最远），作为 chunk C 的向量。
func orthVec() []float32 {
	v := make([]float32, 1024)
	v[500] = 1
	return v
}

// farVec 空态查询：与全部测试向量（unitVec 平面 + orthVec@500）都正交。
func farVec() []float32 {
	v := make([]float32, 1024)
	v[600] = 1
	return v
}

func TestHybridSearchSQL(t *testing.T) {
	db := openTestDB(t)
	truncateAll(db)
	ctx := context.Background()

	// 直接落用户（chunks 检索为 repo 层测试，无需走 HTTP）
	var userID int64
	require.NoError(t, db.Raw(`INSERT INTO users (username, password_hash, is_admin, status) VALUES ('m5root', 'x', TRUE, 1) RETURNING id`).Scan(&userID).Error)
	require.Greater(t, userID, int64(0))

	// 建库与文档
	kb := &model.KnowledgeBase{Name: "检索库", CreatedBy: userID}
	require.NoError(t, db.Create(kb).Error)
	doc := &model.Document{KBID: kb.ID, Type: model.DocTypeUpload, Name: "m5", Status: model.DocStatusReady, CreatedBy: userID}
	require.NoError(t, db.Create(doc).Error)

	// 三条分块：A 与查询向量近似；B 关键词命中；C 既不近似也无关键词（不应命中）
	idA := seedChunk(t, db, kb.ID, doc.ID, 0, "限流中间件实现，使用固定窗口算法", "internal/middleware/ratelimit.go", unitVec(0.95))
	idB := seedChunk(t, db, kb.ID, doc.ID, 1, "完全不同的内容关于部署流程", "docs/deploy.md", unitVec(0.05))
	idC := seedChunk(t, db, kb.ID, doc.ID, 2, "无关内容其它", "other/x.txt", orthVec())

	sr := repo.NewSearchRepo(db)

	// 查询：向量近似 A；关键词 "限流" 仅 A 命中（trigram）
	hits, err := sr.HybridSearch(ctx, kb.ID, unitVec(0.9), "限流", 50)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, idA, hits[0].ChunkID, "向量+关键词双命中的 A 排第一")
	// A 的 RRF 分数 = 1/(60+1) + 1/(60+1) ≈ 0.0328
	assert.Greater(t, hits[0].Score, 0.03)
	foundB := false
	for _, h := range hits {
		assert.NotEqual(t, idC, h.ChunkID, "C 不应命中")
		if h.ChunkID == idB {
			foundB = true // B 可能因 trigram 部分匹配进入兜底（similarity > 0）
		}
	}
	_ = foundB

	// 快照字段
	assert.Equal(t, "internal/middleware/ratelimit.go", *hits[0].FilePath)
	assert.NotNil(t, hits[0].LineStart)
	assert.Equal(t, 10, *hits[0].LineStart)
	assert.Equal(t, int64(doc.ID), hits[0].DocumentID)

	// 仅关键词（无向量命中场景：随机向量远）——trigram 兜底
	hits, err = sr.HybridSearch(ctx, kb.ID, unitVec(0.02), "限流", 50)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	assert.Equal(t, idA, hits[0].ChunkID, "关键词命中兜底")

	// 空态：与全部向量正交的查询 + 不相关词 → 空结果
	hits, err = sr.HybridSearch(ctx, kb.ID, farVec(), "不存在词组zzz", 50)
	require.NoError(t, err)
	assert.Empty(t, hits, "空态：items 为空")

	// 分块计数
	n, err := sr.CountReadyChunks(ctx, kb.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)

	// 库隔离：另一库的 chunks 不可见
	kb2 := &model.KnowledgeBase{Name: "其他库", CreatedBy: userID}
	require.NoError(t, db.Create(kb2).Error)
	doc2 := &model.Document{KBID: kb2.ID, Type: model.DocTypeUpload, Name: "d2", Status: model.DocStatusReady, CreatedBy: userID}
	require.NoError(t, db.Create(doc2).Error)
	seedChunk(t, db, kb2.ID, doc2.ID, 0, "限流内容", "a.go", unitVec(0.9))
	hits, err = sr.HybridSearch(ctx, kb.ID, unitVec(0.9), "限流", 50)
	require.NoError(t, err)
	for _, h := range hits {
		assert.NotEqual(t, int64(doc2.ID), h.DocumentID, "库隔离")
	}

	fmt.Println("hybrid search integration done")
}
