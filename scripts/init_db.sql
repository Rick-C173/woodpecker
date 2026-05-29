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

-- 索引
CREATE INDEX IF NOT EXISTS idx_reviews_project_id ON reviews(project_id);
CREATE INDEX IF NOT EXISTS idx_reviews_pr_number ON reviews(pr_number);
CREATE INDEX IF NOT EXISTS idx_reviews_commit_sha ON reviews(commit_sha);
CREATE INDEX IF NOT EXISTS idx_reviews_created_at ON reviews(created_at);
CREATE INDEX IF NOT EXISTS idx_review_comments_review_id ON review_comments(review_id);
