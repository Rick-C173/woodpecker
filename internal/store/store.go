package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"woodpecker/config"
)

type Store struct {
	db        *pgxpool.Pool
	Reviews   *ReviewRepository
	Knowledge *KnowledgeRepository
}

func NewStore(cfg config.DatabaseConfig) (*Store, error) {
	dsn := cfg.DSN()

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}

	poolConfig.MaxConns = int32(cfg.MaxConns)

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	store := &Store{
		db:        pool,
		Reviews:   NewReviewRepository(pool),
		Knowledge: NewKnowledgeRepository(pool),
	}

	if err := store.Migrate(); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return store, nil
}

func (s *Store) Close() {
	s.db.Close()
}

func (s *Store) Migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS projects (
		id SERIAL PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		owner VARCHAR(255) NOT NULL,
		repo VARCHAR(255) NOT NULL,
		webhook_secret VARCHAR(255),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(owner, repo)
	);

	CREATE TABLE IF NOT EXISTS reviews (
		id SERIAL PRIMARY KEY,
		project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
		pr_number INTEGER NOT NULL,
		pr_title VARCHAR(500),
		pr_url VARCHAR(500),
		commit_sha VARCHAR(40) NOT NULL,
		branch VARCHAR(255),
		base_branch VARCHAR(255),
		diff_text TEXT,
		language VARCHAR(50),
		summary TEXT,
		total_files INTEGER DEFAULT 0,
		total_issues INTEGER DEFAULT 0,
		status VARCHAR(50) DEFAULT 'pending',
		error_message TEXT,
		prompt_tokens INTEGER DEFAULT 0,
		completion_tokens INTEGER DEFAULT 0,
		reviewer_type VARCHAR(50) DEFAULT 'llm',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS review_comments (
		id SERIAL PRIMARY KEY,
		review_id INTEGER REFERENCES reviews(id) ON DELETE CASCADE,
		file_path VARCHAR(500) NOT NULL,
		line_number INTEGER,
		category VARCHAR(50) NOT NULL,
		severity VARCHAR(50) NOT NULL,
		message TEXT NOT NULL,
		suggestion TEXT,
		confidence FLOAT,
		rule_id VARCHAR(100),
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS knowledge_index (
		id SERIAL PRIMARY KEY,
		repo_owner VARCHAR(255) NOT NULL,
		repo_name VARCHAR(255) NOT NULL,
		last_indexed_at TIMESTAMP,
		status VARCHAR(50),
		total_chunks INTEGER DEFAULT 0,
		error_message TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(repo_owner, repo_name)
	);

	CREATE TABLE IF NOT EXISTS qa_history (
		id SERIAL PRIMARY KEY,
		repo_owner VARCHAR(255),
		repo_name VARCHAR(255),
		query TEXT NOT NULL,
		answer TEXT,
		sources JSONB,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_reviews_project_id ON reviews(project_id);
	CREATE INDEX IF NOT EXISTS idx_reviews_pr_number ON reviews(pr_number);
	CREATE INDEX IF NOT EXISTS idx_reviews_commit_sha ON reviews(commit_sha);
	CREATE INDEX IF NOT EXISTS idx_reviews_created_at ON reviews(created_at);
	CREATE INDEX IF NOT EXISTS idx_review_comments_review_id ON review_comments(review_id);
	CREATE INDEX IF NOT EXISTS idx_qa_history_repo ON qa_history(repo_owner, repo_name);
	`

	_, err := s.db.Exec(context.Background(), schema)
	return err
}
