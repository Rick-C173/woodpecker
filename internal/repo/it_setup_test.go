//go:build integration

// 仓储层真库集成测试环境（TestMain）：
//   - 优先读 TEST_DSN（CI 用 service 容器注入）
//   - 缺省连本机 127.0.0.1:5433（docker-compose.dev.yml 备用栈）
//   - 不可达则整体退出（CI 的 backend job 会先跑普通单测，此处失败可见）
package repo

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"woodpecker/internal/database"
)

var itDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		dsn = "postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker?sslmode=disable"
	}
	db, err := database.Open(dsn, 20, 5, 30)
	if err == nil {
		sqlDB, _ := db.DB()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err = sqlDB.PingContext(ctx)
	}
	if err != nil {
		// CI 里库不可达必须显式失败：os.Exit(0) 会让 9 个仓储用例无声消失而 CI 全绿（审计 §3.4）。
		if os.Getenv("CI") != "" {
			log.Fatalf("集成测试需要 PostgreSQL，CI 环境不得跳过（设置 TEST_DSN）: %v", err)
		}
		log.Printf("跳过仓储集成测试（PostgreSQL 不可达）: %v", err)
		os.Exit(0)
	}
	if err := database.Migrate(dsn); err != nil {
		log.Fatalf("迁移失败: %v", err)
	}
	_ = db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error
	_ = db.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm").Error
	itDB = db
	os.Exit(m.Run())
}

// itTruncate 清空业务表（每测试独立数据）。
// 清单须与 internal/integration/setup_test.go 的 truncateAll 保持一致（共 15 张）。
func itTruncate(t *testing.T) {
	t.Helper()
	err := itDB.Exec(`TRUNCATE users, audit_logs, settings, model_services, model_entries,
		knowledge_bases, kb_members, documents, document_files, ingest_tasks, chunks,
		conversations, messages, message_citations, feedbacks RESTART IDENTITY CASCADE`).Error
	if err != nil {
		t.Fatalf("清空表失败: %v", err)
	}
}

func itCount(t *testing.T, sql string) int64 {
	t.Helper()
	var n int64
	if err := itDB.Raw(sql).Scan(&n).Error; err != nil {
		t.Fatalf("计数查询失败: %v", err)
	}
	return n
}

func itStr(s string) *string { return &s }
