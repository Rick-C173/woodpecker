package store

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"woodpecker/config"
)

// 测试数据库配置 - 使用一个临时数据库
func getTestDBConfigTest() (config.DatabaseConfig, error) {
	host := os.Getenv("WOODPECKER_TEST_DB_HOST")
	if host == "" {
		host = "localhost"
	}

	portStr := os.Getenv("WOODPECKER_TEST_DB_PORT")
	port := 5432
	if portStr != "" {
		fmt.Sscanf(portStr, "%d", &port)
	}

	user := os.Getenv("WOODPECKER_TEST_DB_USER")
	if user == "" {
		user = "postgres"
	}

	password := os.Getenv("WOODPECKER_TEST_DB_PASSWORD")
	if password == "" {
		password = "142857"
	}

	database := os.Getenv("WOODPECKER_TEST_DB_NAME")
	if database == "" {
		database = "woodpecker_test"
	}

	return config.DatabaseConfig{
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		Database: database,
		SSLMode:  "disable",
		MaxConns: 5,
	}, nil
}

// 跳过测试的辅助函数 - 如果没有数据库则跳过
func skipIfNoDBTest(t *testing.T, cfg config.DatabaseConfig) {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), cfg.DSN())
	if err != nil {
		t.Skipf("数据库连接失败，跳过测试: %v", err)
		return
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("数据库不可用，跳过测试: %v", err)
	}
}

func TestNewStore(t *testing.T) {
	cfg, err := getTestDBConfigTest()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBTest(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()

	if store.Reviews == nil {
		t.Error("Reviews 仓库未初始化")
	}
	if store.db == nil {
		t.Error("数据库连接未初始化")
	}
}

func TestStore_Migrate(t *testing.T) {
	cfg, err := getTestDBConfigTest()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBTest(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate 失败: %v", err)
	}

	var count int
	err = store.db.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM information_schema.tables 
		WHERE table_schema = 'public'
	`).Scan(&count)
	if err != nil {
		t.Fatalf("查询表失败: %v", err)
	}
	if count < 3 {
		t.Errorf("期望至少有3个表，实际有 %d 个", count)
	}
}
