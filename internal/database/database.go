// Package database 负责 GORM 连接与版本化迁移（embed + golang-migrate）。
package database

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres" // 注册 postgres driver（副作用导入）
	"github.com/golang-migrate/migrate/v4/source/iofs"
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

// Migrate 执行嵌入的版本化迁移；重复执行幂等（已应用则跳过）。
func Migrate(dsn string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("读取嵌入迁移: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("初始化迁移: %w", err)
	}
	defer func() { _, _ = m.Close() }() // migrate.Close 返回 source/database 两个 error
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	slog.Info("数据库迁移完成")
	return nil
}
