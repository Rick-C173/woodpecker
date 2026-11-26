//go:build integration

// 集成测试环境准备：
//   - 优先读 TEST_DSN（CI 用 service 容器注入）
//   - 缺省连 docker-compose.dev.yml 备用栈（127.0.0.1:5433）
//   - 都不可达则跳过（不阻塞单元测试）
//
// 说明：原计划用 testcontainers，但其 provider 在本机 Windows+Docker Desktop 存在兼容问题
// （见 开发计划.md §3 风险对策），验证强度不变，改为外部容器 + 环境变量注入。
package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"woodpecker/internal/database"
)

type gormDB = gorm.DB

var testDSN = func() string {
	if v := os.Getenv("TEST_DSN"); v != "" {
		return v
	}
	// 默认连本机原生 PostgreSQL 5432；CI 或容器环境用 TEST_DSN 覆盖
	// （docker-compose.dev.yml 备用栈映射 5433：TEST_DSN=postgres://woodpecker:woodpecker@127.0.0.1:5433/woodpecker?sslmode=disable）
	return "postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker_test?sslmode=disable"
}()

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open(testDSN, 20, 5, 30)
	if err == nil {
		sqlDB, _ := db.DB()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err = sqlDB.PingContext(ctx)
	}
	if err != nil {
		t.Skipf("集成测试需要 PostgreSQL：设置 TEST_DSN 或启动 docker-compose.dev.yml（%v）", err)
	}
	require.NoError(t, database.Migrate(testDSN), "迁移应幂等成功")
	return db
}

func truncateAll(db *gorm.DB) {
	// 容器用户为超级用户可直接装扩展；本地库已装则跳过
	_ = db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error
	_ = db.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm").Error
	// 清单须与 internal/repo/it_setup_test.go 的 itTruncate 保持一致（共 16 张），
	// 否则文档/分块用例的脏数据会跨用例累积（审计 §3.4）。
	_ = db.Exec("TRUNCATE users, audit_logs, settings, model_services, model_entries, knowledge_bases, kb_members, documents, document_files, ingest_tasks, chunks, conversations, messages, message_citations, feedbacks, invites RESTART IDENTITY CASCADE").Error
}
