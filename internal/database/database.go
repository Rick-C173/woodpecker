// Package database 负责 GORM 连接与嵌入 SQL 迁移的执行（自研 runner，无第三方迁移依赖）。
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // 注册 pgx driver（无参数 Exec 走简单查询协议，支持多语句）
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"woodpecker/migrations"
)

// Open 建立 GORM 连接；连接池参数来自配置（数据库设计 §6，默认 20/5/30min）。
func Open(dsn string, maxOpen, maxIdle, maxLifetimeMinutes int) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		PrepareStmt: false, // 数据库设计 §6：复杂手写 SQL 为主，关闭预编译缓存
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(time.Duration(maxLifetimeMinutes) * time.Minute)
	return db, nil
}

// Migrate 按文件名字典序执行嵌入的 *.up.sql，已应用文件记录在 applied_migrations 表，
// 重复执行幂等、多进程并发安全（server 与 worker 同时启动不冲突）。
func Migrate(dsn string) error {
	ctx := context.Background() // 启动关键路径无超时约束
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("打开数据库连接: %w", err)
	}
	defer func() { _ = db.Close() }()

	entries, err := fs.ReadDir(migrations.FS, ".") // embed FS 目录已按文件名排序
	if err != nil {
		return fmt.Errorf("读取嵌入迁移: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		content, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("读取迁移 %s: %w", name, err)
		}
		applied, err := applyMigration(ctx, db, name, string(content))
		if err != nil {
			return fmt.Errorf("执行迁移 %s: %w", name, err)
		}
		if applied {
			slog.Info("应用迁移", "file", name)
		}
	}
	slog.Info("数据库迁移完成")
	return nil
}

// applyMigration 单事务内原子抢占并执行一个迁移文件：
// advisory lock（按库名散列，实例内各库互不影响）串行化并发进程；
// INSERT ... ON CONFLICT 抢占记录，抢到者执行文件内容，未抢到说明其他进程已应用，跳过。
func applyMigration(ctx context.Context, db *sql.DB, name, content string) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }() // Commit 后 Rollback 返回 ErrTxDone，无害

	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(current_database()))"); err != nil {
		return false, err
	}
	// 记录表独立于业务 schema（不进业务基线文件）；建表须在 advisory lock 内——
	// 并发 CREATE TABLE IF NOT EXISTS 各自快照判定会撞 pg_type 唯一键
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS applied_migrations (
	name       TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		return false, err
	}
	var claimed bool
	err = tx.QueryRowContext(ctx,
		`INSERT INTO applied_migrations (name) VALUES ($1) ON CONFLICT (name) DO NOTHING RETURNING true`,
		name,
	).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // 其他进程已应用
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, content); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
