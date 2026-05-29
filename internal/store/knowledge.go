package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// KnowledgeIndex 知识索引记录
type KnowledgeIndex struct {
	ID            int64
	RepoOwner     string
	RepoName      string
	LastIndexedAt *time.Time
	Status        string
	TotalChunks   int
	ErrorMessage  string
	CreatedAt     time.Time
}

// QAHistory 问答历史记录
type QAHistory struct {
	ID        int64
	RepoOwner string
	RepoName  string
	Query     string
	Answer    string
	Sources   interface{}
	CreatedAt time.Time
}

// KnowledgeRepository 知识仓储
type KnowledgeRepository struct {
	db *pgxpool.Pool
}

func NewKnowledgeRepository(db *pgxpool.Pool) *KnowledgeRepository {
	return &KnowledgeRepository{db: db}
}

// CreateOrUpdateIndex 创建或更新索引记录
func (r *KnowledgeRepository) CreateOrUpdateIndex(
	ctx context.Context,
	repoOwner, repoName string,
) (*KnowledgeIndex, error) {
	var idx KnowledgeIndex

	err := r.db.QueryRow(ctx, `
		INSERT INTO knowledge_index (repo_owner, repo_name, status)
		VALUES ($1, $2, 'pending')
		ON CONFLICT (repo_owner, repo_name) DO UPDATE SET status = 'pending'
		RETURNING id, repo_owner, repo_name, last_indexed_at, status, total_chunks, error_message, created_at
	`, repoOwner, repoName).Scan(
		&idx.ID, &idx.RepoOwner, &idx.RepoName,
		&idx.LastIndexedAt, &idx.Status, &idx.TotalChunks, &idx.ErrorMessage, &idx.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &idx, nil
}

// UpdateIndexStatus 更新索引状态
func (r *KnowledgeRepository) UpdateIndexStatus(
	ctx context.Context,
	repoOwner, repoName, status string,
	totalChunks int,
	errorMessage string,
) error {
	now := time.Now()
	_, err := r.db.Exec(ctx, `
		UPDATE knowledge_index
		SET status = $1, total_chunks = $2, error_message = $3, last_indexed_at = $4
		WHERE repo_owner = $5 AND repo_name = $6
	`, status, totalChunks, errorMessage, now, repoOwner, repoName)
	return err
}

// GetIndex 获取索引记录
func (r *KnowledgeRepository) GetIndex(
	ctx context.Context,
	repoOwner, repoName string,
) (*KnowledgeIndex, error) {
	var idx KnowledgeIndex

	err := r.db.QueryRow(ctx, `
		SELECT id, repo_owner, repo_name, last_indexed_at, status, total_chunks, error_message, created_at
		FROM knowledge_index
		WHERE repo_owner = $1 AND repo_name = $2
	`, repoOwner, repoName).Scan(
		&idx.ID, &idx.RepoOwner, &idx.RepoName,
		&idx.LastIndexedAt, &idx.Status, &idx.TotalChunks, &idx.ErrorMessage, &idx.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &idx, nil
}

// ListAllIndexes 列出所有索引
func (r *KnowledgeRepository) ListAllIndexes(ctx context.Context) ([]*KnowledgeIndex, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, repo_owner, repo_name, last_indexed_at, status, total_chunks, error_message, created_at
		FROM knowledge_index
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []*KnowledgeIndex
	for rows.Next() {
		var idx KnowledgeIndex
		err := rows.Scan(
			&idx.ID, &idx.RepoOwner, &idx.RepoName,
			&idx.LastIndexedAt, &idx.Status, &idx.TotalChunks, &idx.ErrorMessage, &idx.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, &idx)
	}
	return indexes, nil
}

// DeleteIndex 删除索引记录
func (r *KnowledgeRepository) DeleteIndex(ctx context.Context, repoOwner, repoName string) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM knowledge_index
		WHERE repo_owner = $1 AND repo_name = $2
	`, repoOwner, repoName)
	return err
}

// AddQAHistory 添加问答历史
func (r *KnowledgeRepository) AddQAHistory(
	ctx context.Context,
	repoOwner, repoName, query, answer string,
	sources interface{},
) (*QAHistory, error) {
	var history QAHistory

	sourcesBytes, err := json.Marshal(sources)
	if err != nil {
		return nil, err
	}

	err = r.db.QueryRow(ctx, `
		INSERT INTO qa_history (repo_owner, repo_name, query, answer, sources)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, repo_owner, repo_name, query, answer, sources, created_at
	`, repoOwner, repoName, query, answer, sourcesBytes).Scan(
		&history.ID, &history.RepoOwner, &history.RepoName,
		&history.Query, &history.Answer, &history.Sources, &history.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &history, nil
}

// ListQAHistory 列出问答历史
func (r *KnowledgeRepository) ListQAHistory(
	ctx context.Context,
	repoOwner, repoName string,
	limit int,
) ([]*QAHistory, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := r.db.Query(ctx, `
		SELECT id, repo_owner, repo_name, query, answer, sources, created_at
		FROM qa_history
		WHERE repo_owner = $1 AND repo_name = $2
		ORDER BY created_at DESC
		LIMIT $3
	`, repoOwner, repoName, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var histories []*QAHistory
	for rows.Next() {
		var history QAHistory
		var sourcesBytes []byte
		err := rows.Scan(
			&history.ID, &history.RepoOwner, &history.RepoName,
			&history.Query, &history.Answer, &sourcesBytes, &history.CreatedAt,
		)
		if err != nil {
			return nil, err
		}

		if len(sourcesBytes) > 0 {
			var sources interface{}
			if err := json.Unmarshal(sourcesBytes, &sources); err == nil {
				history.Sources = sources
			}
		}
		histories = append(histories, &history)
	}
	return histories, nil
}
