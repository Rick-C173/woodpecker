-- Woodpecker 数据库初始化脚本
-- 创建数据库：CREATE DATABASE woodpecker;

-- 项目表：存储 GitHub 仓库信息
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

-- 审查记录表：存储每次审查的结果
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

-- 审查评论表：存储每条审查意见
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

-- 知识索引状态表：存储仓库代码索引状态
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

-- 问答历史表：存储自然语言查询历史
CREATE TABLE IF NOT EXISTS qa_history (
    id SERIAL PRIMARY KEY,
    repo_owner VARCHAR(255),
    repo_name VARCHAR(255),
    query TEXT NOT NULL,
    answer TEXT,
    sources JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_reviews_project_id ON reviews(project_id);
CREATE INDEX IF NOT EXISTS idx_reviews_pr_number ON reviews(pr_number);
CREATE INDEX IF NOT EXISTS idx_reviews_commit_sha ON reviews(commit_sha);
CREATE INDEX IF NOT EXISTS idx_reviews_created_at ON reviews(created_at);
CREATE INDEX IF NOT EXISTS idx_review_comments_review_id ON review_comments(review_id);
CREATE INDEX IF NOT EXISTS idx_qa_history_repo ON qa_history(repo_owner, repo_name);

-- 向量扩展（如果使用 pgvector）
-- CREATE EXTENSION IF NOT EXISTS vector;

-- 代码块向量表（如果使用 pgvector 替代单独的向量数据库）
-- CREATE TABLE IF NOT EXISTS code_chunks (
--     id VARCHAR(255) PRIMARY KEY,
--     repo_owner VARCHAR(255) NOT NULL,
--     repo_name VARCHAR(255) NOT NULL,
--     file_path VARCHAR(500) NOT NULL,
--     language VARCHAR(50),
--     chunk_type VARCHAR(50),
--     start_line INTEGER,
--     end_line INTEGER,
--     symbol_name VARCHAR(255),
--     content TEXT,
--     embedding vector(1536),
--     indexed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
-- );
-- 
-- CREATE INDEX IF NOT EXISTS idx_code_chunks_repo ON code_chunks(repo_owner, repo_name);
-- CREATE INDEX IF NOT EXISTS idx_code_chunks_embedding ON code_chunks USING hnsw(embedding vector_cosine_ops);
