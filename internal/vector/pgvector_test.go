package vector

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"woodpecker/config"
)

func getTestDBConfig() (config.DatabaseConfig, error) {
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

func skipIfNoDB(t *testing.T) bool {
	t.Helper()

	dbCfg, err := getTestDBConfig()
	if err != nil {
		t.Skipf("获取测试数据库配置失败: %v", err)
		return true
	}

	pool, err := pgxpool.New(context.Background(), dbCfg.DSN())
	if err != nil {
		t.Skipf("数据库连接失败，跳过测试: %v", err)
		return true
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("数据库不可用，跳过测试: %v", err)
		return true
	}

	return false
}

func cleanTestTable(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), "TRUNCATE TABLE code_chunks CASCADE")
	if err != nil {
		t.Logf("清理测试表失败: %v", err)
	}
}

func TestPGStore_Initialize(t *testing.T) {
	if skipIfNoDB(t) {
		return
	}

	dbCfg, _ := getTestDBConfig()
	store, err := NewPGStore(dbCfg, 1536)
	if err != nil {
		t.Fatalf("NewPGStore failed: %v", err)
	}
	defer store.Close(context.Background())

	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
}

func TestPGStore_UpsertAndSearch(t *testing.T) {
	if skipIfNoDB(t) {
		return
	}

	dbCfg, _ := getTestDBConfig()
	store, err := NewPGStore(dbCfg, 1536)
	if err != nil {
		t.Fatalf("NewPGStore failed: %v", err)
	}
	defer store.Close(context.Background())

	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	cleanTestTable(t, store.pool)

	chunk := &CodeChunk{
		ID:         "test-chunk-1",
		RepoOwner:  "test-owner",
		RepoName:   "test-repo",
		FilePath:   "main.go",
		Language:   "go",
		ChunkType:  "function",
		StartLine:  1,
		EndLine:    10,
		SymbolName: "main",
		Content:    "func main() { println(\"Hello\") }",
		IndexedAt:  time.Now(),
	}

	vector := make([]float32, 1536)
	for i := range vector {
		vector[i] = 0.1
	}

	if err := store.Upsert(context.Background(), chunk, vector); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	results, err := store.Search(context.Background(), vector, nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Search returned no results")
	}

	if results[0].Chunk.ID != "test-chunk-1" {
		t.Errorf("Expected chunk ID 'test-chunk-1', got '%s'", results[0].Chunk.ID)
	}
}

func TestPGStore_SearchWithFilter(t *testing.T) {
	if skipIfNoDB(t) {
		return
	}

	dbCfg, _ := getTestDBConfig()
	store, err := NewPGStore(dbCfg, 1536)
	if err != nil {
		t.Fatalf("NewPGStore failed: %v", err)
	}
	defer store.Close(context.Background())

	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	cleanTestTable(t, store.pool)

	vector := make([]float32, 1536)
	for i := range vector {
		vector[i] = 0.1
	}

	for i := 0; i < 3; i++ {
		chunk := &CodeChunk{
			ID:         fmt.Sprintf("test-chunk-%d", i),
			RepoOwner:  "test-owner",
			RepoName:   fmt.Sprintf("test-repo-%d", i),
			FilePath:   fmt.Sprintf("main%d.go", i),
			Language:   "go",
			ChunkType:  "function",
			StartLine:  1,
			EndLine:    10,
			SymbolName: fmt.Sprintf("func%d", i),
			Content:    fmt.Sprintf("func test%d() {}", i),
			IndexedAt:  time.Now(),
		}
		if err := store.Upsert(context.Background(), chunk, vector); err != nil {
			t.Fatalf("Upsert %d failed: %v", i, err)
		}
	}

	filter := &Filter{
		RepoOwner: "test-owner",
		Limit:     10,
	}

	results, err := store.Search(context.Background(), vector, filter)
	if err != nil {
		t.Fatalf("Search with filter failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}
}

func TestPGStore_Delete(t *testing.T) {
	if skipIfNoDB(t) {
		return
	}

	dbCfg, _ := getTestDBConfig()
	store, err := NewPGStore(dbCfg, 1536)
	if err != nil {
		t.Fatalf("NewPGStore failed: %v", err)
	}
	defer store.Close(context.Background())

	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	cleanTestTable(t, store.pool)

	vector := make([]float32, 1536)
	for i := range vector {
		vector[i] = 0.1
	}

	chunk := &CodeChunk{
		ID:        "delete-test",
		RepoOwner: "test-owner",
		RepoName:  "test-repo",
		FilePath:  "delete.go",
		Language:  "go",
		ChunkType: "function",
		Content:   "func delete() {}",
		IndexedAt: time.Now(),
	}

	if err := store.Upsert(context.Background(), chunk, vector); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}

	if err := store.Delete(context.Background(), []string{"delete-test"}); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	filter := &Filter{RepoOwner: "test-owner"}
	count, err := store.Count(context.Background(), filter)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected count 0 after delete, got %d", count)
	}
}

func TestPGStore_Count(t *testing.T) {
	if skipIfNoDB(t) {
		return
	}

	dbCfg, _ := getTestDBConfig()
	store, err := NewPGStore(dbCfg, 1536)
	if err != nil {
		t.Fatalf("NewPGStore failed: %v", err)
	}
	defer store.Close(context.Background())

	if err := store.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	cleanTestTable(t, store.pool)

	vector := make([]float32, 1536)
	for i := range vector {
		vector[i] = 0.1
	}

	for i := 0; i < 5; i++ {
		chunk := &CodeChunk{
			ID:        fmt.Sprintf("count-test-%d", i),
			RepoOwner: "test-owner",
			RepoName:  "test-repo",
			FilePath:  fmt.Sprintf("count%d.go", i),
			Language:  "go",
			Content:   fmt.Sprintf("func count%d() {}", i),
			IndexedAt: time.Now(),
		}
		if err := store.Upsert(context.Background(), chunk, vector); err != nil {
			t.Fatalf("Upsert %d failed: %v", i, err)
		}
	}

	filter := &Filter{
		RepoOwner: "test-owner",
		RepoName:  "test-repo",
	}

	count, err := store.Count(context.Background(), filter)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}

	if count != 5 {
		t.Errorf("Expected count 5, got %d", count)
	}
}
