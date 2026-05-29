package vector

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"woodpecker/config"
)

type PGStore struct {
	pool      *pgxpool.Pool
	dimension int
	tableName string
}

func NewPGStore(dbCfg config.DatabaseConfig, dimension int) (*PGStore, error) {
	poolConfig, err := pgxpool.ParseConfig(dbCfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	poolConfig.MaxConns = int32(dbCfg.MaxConns)

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &PGStore{
		pool:      pool,
		dimension: dimension,
		tableName: "code_chunks",
	}, nil
}

func (s *PGStore) Initialize(ctx context.Context) error {
	// 启用 pgvector 扩展
	_, err := s.pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		return fmt.Errorf("create vector extension: %w", err)
	}

	// 创建表
	createTableSQL := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id VARCHAR(255) PRIMARY KEY,
			repo_owner VARCHAR(255) NOT NULL,
			repo_name VARCHAR(255) NOT NULL,
			file_path VARCHAR(500) NOT NULL,
			language VARCHAR(50),
			chunk_type VARCHAR(50),
			start_line INTEGER,
			end_line INTEGER,
			symbol_name VARCHAR(255),
			content TEXT,
			embedding vector(%d),
			indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`, s.tableName, s.dimension)

	_, err = s.pool.Exec(ctx, createTableSQL)
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	// 创建索引
	createIndexSQL := fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx_%s_embedding 
		ON %s USING hnsw (embedding vector_cosine_ops)
	`, s.tableName, s.tableName)

	_, err = s.pool.Exec(ctx, createIndexSQL)
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	// 创建元数据索引
	_, err = s.pool.Exec(ctx, fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS idx_%s_repo 
		ON %s (repo_owner, repo_name)
	`, s.tableName, s.tableName))
	if err != nil {
		return fmt.Errorf("create repo index: %w", err)
	}

	return nil
}

func (s *PGStore) Upsert(ctx context.Context, chunk *CodeChunk, vector []float32) error {
	embedding := pgvector.NewVector(vector)

	_, err := s.pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, repo_owner, repo_name, file_path, language, chunk_type, 
						start_line, end_line, symbol_name, content, embedding, indexed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO UPDATE SET
			repo_owner = EXCLUDED.repo_owner,
			repo_name = EXCLUDED.repo_name,
			file_path = EXCLUDED.file_path,
			language = EXCLUDED.language,
			chunk_type = EXCLUDED.chunk_type,
			start_line = EXCLUDED.start_line,
			end_line = EXCLUDED.end_line,
			symbol_name = EXCLUDED.symbol_name,
			content = EXCLUDED.content,
			embedding = EXCLUDED.embedding,
			indexed_at = EXCLUDED.indexed_at
	`, s.tableName),
		chunk.ID,
		chunk.RepoOwner,
		chunk.RepoName,
		chunk.FilePath,
		chunk.Language,
		chunk.ChunkType,
		chunk.StartLine,
		chunk.EndLine,
		chunk.SymbolName,
		chunk.Content,
		embedding,
		chunk.IndexedAt,
	)

	return err
}

func (s *PGStore) UpsertBatch(ctx context.Context, chunks []*CodeChunk, vectors [][]float32) error {
	if len(chunks) != len(vectors) {
		return fmt.Errorf("chunks and vectors count mismatch: %d vs %d", len(chunks), len(vectors))
	}

	batch := &pgx.Batch{}

	for i, chunk := range chunks {
		embedding := pgvector.NewVector(vectors[i])

		batch.Queue(fmt.Sprintf(`
			INSERT INTO %s (id, repo_owner, repo_name, file_path, language, chunk_type, 
							start_line, end_line, symbol_name, content, embedding, indexed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (id) DO UPDATE SET
				repo_owner = EXCLUDED.repo_owner,
				repo_name = EXCLUDED.repo_name,
				file_path = EXCLUDED.file_path,
				language = EXCLUDED.language,
				chunk_type = EXCLUDED.chunk_type,
				start_line = EXCLUDED.start_line,
				end_line = EXCLUDED.end_line,
				symbol_name = EXCLUDED.symbol_name,
				content = EXCLUDED.content,
				embedding = EXCLUDED.embedding,
				indexed_at = EXCLUDED.indexed_at
		`, s.tableName),
			chunk.ID,
			chunk.RepoOwner,
			chunk.RepoName,
			chunk.FilePath,
			chunk.Language,
			chunk.ChunkType,
			chunk.StartLine,
			chunk.EndLine,
			chunk.SymbolName,
			chunk.Content,
			embedding,
			chunk.IndexedAt,
		)
	}

	results := s.pool.SendBatch(ctx, batch)
	defer results.Close()

	for range chunks {
		_, err := results.Exec()
		if err != nil {
			return fmt.Errorf("batch insert: %w", err)
		}
	}

	return nil
}

func (s *PGStore) Search(ctx context.Context, queryVector []float32, filter *Filter) ([]*SearchResult, error) {
	if filter == nil {
		filter = &Filter{Limit: 5}
	}
	if filter.Limit == 0 {
		filter.Limit = 5
	}

	embedding := pgvector.NewVector(queryVector)

	// 构建 WHERE 子句
	whereClause, args := s.buildWhereClause(filter)

	query := fmt.Sprintf(`
		SELECT id, repo_owner, repo_name, file_path, language, chunk_type,
			   start_line, end_line, symbol_name, content, indexed_at,
			   1 - (embedding <=> $1) as score
		FROM %s
		%s
		ORDER BY embedding <=> $1
		LIMIT $%d
	`, s.tableName, whereClause, len(args)+1)

	args = append([]any{embedding}, args...)
	args = append(args, filter.Limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	var results []*SearchResult
	for rows.Next() {
		var result SearchResult
		var chunk CodeChunk

		err := rows.Scan(
			&chunk.ID,
			&chunk.RepoOwner,
			&chunk.RepoName,
			&chunk.FilePath,
			&chunk.Language,
			&chunk.ChunkType,
			&chunk.StartLine,
			&chunk.EndLine,
			&chunk.SymbolName,
			&chunk.Content,
			&chunk.IndexedAt,
			&result.Score,
		)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		result.Chunk = &chunk
		results = append(results, &result)
	}

	return results, rows.Err()
}

func (s *PGStore) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE id IN (%s)", s.tableName, strings.Join(placeholders, ","))
	_, err := s.pool.Exec(ctx, query, args...)
	return err
}

func (s *PGStore) DeleteByFilter(ctx context.Context, filter *Filter) error {
	whereClause, args := s.buildWhereClause(filter)
	if whereClause == "" {
		return fmt.Errorf("filter cannot be empty for DeleteByFilter")
	}

	query := fmt.Sprintf("DELETE FROM %s %s", s.tableName, whereClause)
	_, err := s.pool.Exec(ctx, query, args...)
	return err
}

func (s *PGStore) Count(ctx context.Context, filter *Filter) (int64, error) {
	whereClause, args := s.buildWhereClause(filter)

	query := fmt.Sprintf("SELECT COUNT(*) FROM %s %s", s.tableName, whereClause)

	var count int64
	err := s.pool.QueryRow(ctx, query, args...).Scan(&count)
	return count, err
}

func (s *PGStore) Close(ctx context.Context) error {
	s.pool.Close()
	return nil
}

func (s *PGStore) buildWhereClause(filter *Filter) (string, []any) {
	if filter == nil {
		return "", nil
	}

	conditions := []string{}
	args := []any{}
	argIdx := 2

	if filter.RepoOwner != "" {
		conditions = append(conditions, fmt.Sprintf("repo_owner = $%d", argIdx))
		args = append(args, filter.RepoOwner)
		argIdx++
	}

	if filter.RepoName != "" {
		conditions = append(conditions, fmt.Sprintf("repo_name = $%d", argIdx))
		args = append(args, filter.RepoName)
		argIdx++
	}

	if filter.FilePath != "" {
		conditions = append(conditions, fmt.Sprintf("file_path = $%d", argIdx))
		args = append(args, filter.FilePath)
		argIdx++
	}

	if filter.Language != "" {
		conditions = append(conditions, fmt.Sprintf("language = $%d", argIdx))
		args = append(args, filter.Language)
		argIdx++
	}

	if len(filter.ChunkTypes) > 0 {
		placeholders := make([]string, len(filter.ChunkTypes))
		for i, ct := range filter.ChunkTypes {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, ct)
			argIdx++
		}
		conditions = append(conditions, fmt.Sprintf("chunk_type IN (%s)", strings.Join(placeholders, ",")))
	}

	if len(conditions) == 0 {
		return "", nil
	}

	return "WHERE " + strings.Join(conditions, " AND "), args
}
