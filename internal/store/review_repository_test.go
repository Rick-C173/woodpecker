package store

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"woodpecker/config"
	"woodpecker/internal/model"
)

func getTestDBConfigLocal() (config.DatabaseConfig, error) {
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

func skipIfNoDBLocal(t *testing.T, cfg config.DatabaseConfig) {
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

func cleanTestTablesLocal(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := db.Exec(ctx, `
		TRUNCATE TABLE review_comments, reviews, projects CASCADE
	`)
	if err != nil {
		t.Logf("清理测试表失败: %v", err)
	}
}

func TestReviewRepository_SaveProject(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	repo := &Project{
		Name:          "test-repo",
		Owner:         "test-owner",
		Repo:          "test-repo",
		WebhookSecret: "test-secret",
	}

	id, err := store.Reviews.SaveProject(context.Background(), repo)
	if err != nil {
		t.Fatalf("SaveProject 失败: %v", err)
	}
	if id <= 0 {
		t.Errorf("期望返回有效的项目ID，实际为 %d", id)
	}

	id2, err := store.Reviews.SaveProject(context.Background(), repo)
	if err != nil {
		t.Fatalf("SaveProject 更新失败: %v", err)
	}
	if id != id2 {
		t.Errorf("重复保存应该返回相同ID，期望 %d，实际 %d", id, id2)
	}
}

func TestReviewRepository_GetProjectByOwnerRepo(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	repo := &Project{
		Name:  "get-test-repo",
		Owner: "get-test-owner",
		Repo:  "get-test-repo",
	}
	id, err := store.Reviews.SaveProject(context.Background(), repo)
	if err != nil {
		t.Fatalf("SaveProject 失败: %v", err)
	}

	found, err := store.Reviews.GetProjectByOwnerRepo(context.Background(), repo.Owner, repo.Repo)
	if err != nil {
		t.Fatalf("GetProjectByOwnerRepo 失败: %v", err)
	}
	if found == nil {
		t.Fatal("期望找到项目，实际未找到")
	}
	if found.ID != id {
		t.Errorf("ID不匹配，期望 %d，实际 %d", id, found.ID)
	}
	if found.Owner != repo.Owner {
		t.Errorf("Owner不匹配，期望 %s，实际 %s", repo.Owner, found.Owner)
	}

	notFound, err := store.Reviews.GetProjectByOwnerRepo(context.Background(), "nonexistent", "nonexistent")
	if err != nil {
		t.Fatalf("查询不存在项目失败: %v", err)
	}
	if notFound != nil {
		t.Error("期望返回nil，实际返回了项目")
	}
}

func TestReviewRepository_GetProjects(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	for i := 0; i < 3; i++ {
		repo := &Project{
			Name:  fmt.Sprintf("test-repo-%d", i),
			Owner: "test-owner",
			Repo:  fmt.Sprintf("test-repo-%d", i),
		}
		_, err := store.Reviews.SaveProject(context.Background(), repo)
		if err != nil {
			t.Fatalf("SaveProject %d 失败: %v", i, err)
		}
	}

	projects, err := store.Reviews.GetProjects(context.Background())
	if err != nil {
		t.Fatalf("GetProjects 失败: %v", err)
	}
	if len(projects) != 3 {
		t.Errorf("期望3个项目，实际 %d 个", len(projects))
	}
}

func TestReviewRepository_SaveReview(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	project := &Project{
		Name:  "review-test",
		Owner: "review-owner",
		Repo:  "review-test",
	}
	projectID, err := store.Reviews.SaveProject(context.Background(), project)
	if err != nil {
		t.Fatalf("SaveProject 失败: %v", err)
	}

	review := &Review{
		ProjectID:        projectID,
		PRNumber:         123,
		PRTitle:          "Test PR",
		PRURL:            "https://github.com/test/pull/123",
		CommitSHA:        "abc123def456",
		Branch:           "feature/test",
		BaseBranch:       "main",
		DiffText:         "diff --git a/test.go b/test.go",
		Language:         "go",
		Summary:          "发现1个问题",
		TotalFiles:       1,
		TotalIssues:      1,
		Status:           "success",
		PromptTokens:     100,
		CompletionTokens: 50,
		ReviewerType:     "llm",
	}

	reviewID, err := store.Reviews.SaveReview(context.Background(), review)
	if err != nil {
		t.Fatalf("SaveReview 失败: %v", err)
	}
	if reviewID <= 0 {
		t.Error("期望返回有效的审查ID")
	}
	if review.ID != reviewID {
		t.Error("review.ID 应该被设置")
	}
}

func TestReviewRepository_GetReviewByID(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	project := &Project{
		Name:  "get-review-test",
		Owner: "get-review-owner",
		Repo:  "get-review-test",
	}
	projectID, _ := store.Reviews.SaveProject(context.Background(), project)

	review := &Review{
		ProjectID: projectID,
		PRNumber:  456,
		PRTitle:   "Get Review Test",
		CommitSHA: "000000000000",
		Summary:   "测试获取",
		Status:    "success",
	}
	reviewID, _ := store.Reviews.SaveReview(context.Background(), review)

	found, err := store.Reviews.GetReviewByID(context.Background(), reviewID)
	if err != nil {
		t.Fatalf("GetReviewByID 失败: %v", err)
	}
	if found == nil {
		t.Fatal("期望找到审查，实际未找到")
	}
	if found.PRNumber != 456 {
		t.Errorf("PRNumber不匹配，期望456，实际 %d", found.PRNumber)
	}
	if !found.CreatedAt.IsZero() {
		t.Logf("CreatedAt: %v", found.CreatedAt)
	}

	notFound, err := store.Reviews.GetReviewByID(context.Background(), 999999)
	if err != nil {
		t.Fatalf("查询不存在的审查失败: %v", err)
	}
	if notFound != nil {
		t.Error("期望返回nil")
	}
}

func TestReviewRepository_SaveComment(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	project := &Project{
		Name:  "comment-test",
		Owner: "comment-owner",
		Repo:  "comment-test",
	}
	projectID, _ := store.Reviews.SaveProject(context.Background(), project)

	review := &Review{
		ProjectID: projectID,
		PRNumber:  789,
		CommitSHA: "aaaaaaaaaaaa",
		Summary:   "测试评论",
		Status:    "success",
	}
	reviewID, _ := store.Reviews.SaveReview(context.Background(), review)

	comment := &model.ReviewComment{
		FilePath:   "main.go",
		Line:       42,
		Category:   "bug",
		Severity:   "critical",
		Message:    "未处理错误",
		Suggestion: "if err != nil { return }",
		Confidence: 0.95,
		RuleID:     "GO-E001",
	}

	err = store.Reviews.SaveComment(context.Background(), reviewID, comment)
	if err != nil {
		t.Fatalf("SaveComment 失败: %v", err)
	}
}

func TestReviewRepository_GetCommentsByReviewID(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	project := &Project{
		Name:  "get-comments-test",
		Owner: "get-comments-owner",
		Repo:  "get-comments-test",
	}
	projectID, _ := store.Reviews.SaveProject(context.Background(), project)

	review := &Review{
		ProjectID: projectID,
		PRNumber:  111,
		CommitSHA: "bbbbbbbbbbbb",
		Summary:   "测试获取评论",
		Status:    "success",
	}
	reviewID, _ := store.Reviews.SaveReview(context.Background(), review)

	for i := 0; i < 3; i++ {
		comment := &model.ReviewComment{
			FilePath: fmt.Sprintf("file%d.go", i),
			Line:     i * 10,
			Category: "bug",
			Severity: "warning",
			Message:  fmt.Sprintf("测试评论 %d", i),
		}
		_ = store.Reviews.SaveComment(context.Background(), reviewID, comment)
	}

	comments, err := store.Reviews.GetCommentsByReviewID(context.Background(), reviewID)
	if err != nil {
		t.Fatalf("GetCommentsByReviewID 失败: %v", err)
	}
	if len(comments) != 3 {
		t.Errorf("期望3条评论，实际 %d 条", len(comments))
	}

	if comments[0].FilePath != "file0.go" {
		t.Errorf("第一条评论FilePath不匹配，期望 file0.go，实际 %s", comments[0].FilePath)
	}
}

func TestReviewRepository_ListReviews(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	project := &Project{
		Name:  "list-reviews-test",
		Owner: "list-reviews-owner",
		Repo:  "list-reviews-test",
	}
	projectID, _ := store.Reviews.SaveProject(context.Background(), project)

	for i := 0; i < 5; i++ {
		review := &Review{
			ProjectID: projectID,
			PRNumber:  i,
			CommitSHA: fmt.Sprintf("commit%d", i),
			Summary:   fmt.Sprintf("审查 %d", i),
			Status:    "success",
		}
		_, _ = store.Reviews.SaveReview(context.Background(), review)
	}

	filter := ListReviewsFilter{
		ProjectID: &projectID,
		Limit:     2,
		Offset:    0,
	}
	reviews, err := store.Reviews.ListReviews(context.Background(), filter)
	if err != nil {
		t.Fatalf("ListReviews 失败: %v", err)
	}
	if len(reviews) != 2 {
		t.Errorf("期望2条记录，实际 %d 条", len(reviews))
	}

	prNumber := 3
	filter2 := ListReviewsFilter{
		ProjectID: &projectID,
		PRNumber:  &prNumber,
	}
	reviews2, err := store.Reviews.ListReviews(context.Background(), filter2)
	if err != nil {
		t.Fatalf("ListReviews 按PR过滤失败: %v", err)
	}
	if len(reviews2) != 1 {
		t.Errorf("期望1条记录，实际 %d 条", len(reviews2))
	}
}

func TestReviewRepository_GetReviewStats(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	project := &Project{
		Name:  "stats-test",
		Owner: "stats-owner",
		Repo:  "stats-test",
	}
	projectID, _ := store.Reviews.SaveProject(context.Background(), project)

	for i := 0; i < 3; i++ {
		review := &Review{
			ProjectID:        projectID,
			PRNumber:         i,
			CommitSHA:        fmt.Sprintf("s%d", i),
			Summary:          "统计测试",
			TotalIssues:      i + 1,
			Status:           "success",
			PromptTokens:     100,
			CompletionTokens: 50,
		}
		_, _ = store.Reviews.SaveReview(context.Background(), review)
	}

	stats, err := store.Reviews.GetReviewStats(context.Background(), &projectID)
	if err != nil {
		t.Fatalf("GetReviewStats 失败: %v", err)
	}

	if stats["total_reviews"] != 3 {
		t.Errorf("total_reviews 期望3，实际 %d", stats["total_reviews"])
	}
	if stats["successful_reviews"] != 3 {
		t.Errorf("successful_reviews 期望3，实际 %d", stats["successful_reviews"])
	}

	globalStats, err := store.Reviews.GetReviewStats(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetReviewStats 全局统计失败: %v", err)
	}
	if globalStats["total_reviews"] == 0 {
		t.Error("全局统计应该有记录")
	}
}

func TestReviewRepository_UpdateReview(t *testing.T) {
	cfg, err := getTestDBConfigLocal()
	if err != nil {
		t.Fatalf("获取测试数据库配置失败: %v", err)
	}
	skipIfNoDBLocal(t, cfg)

	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore 失败: %v", err)
	}
	defer store.Close()
	cleanTestTablesLocal(t, store.db)

	project := &Project{
		Name:  "update-test",
		Owner: "update-owner",
		Repo:  "update-test",
	}
	projectID, _ := store.Reviews.SaveProject(context.Background(), project)

	review := &Review{
		ProjectID: projectID,
		PRNumber:  999,
		CommitSHA: "initial",
		Summary:   "初始状态",
		Status:    "pending",
	}
	reviewID, _ := store.Reviews.SaveReview(context.Background(), review)

	review.Summary = "已更新"
	review.Status = "success"
	review.TotalIssues = 5
	err = store.Reviews.UpdateReview(context.Background(), reviewID, review)
	if err != nil {
		t.Fatalf("UpdateReview 失败: %v", err)
	}

	updated, _ := store.Reviews.GetReviewByID(context.Background(), reviewID)
	if updated.Summary != "已更新" {
		t.Errorf("Summary未更新，期望 '已更新'，实际 '%s'", updated.Summary)
	}
	if updated.Status != "success" {
		t.Errorf("Status未更新，期望 success，实际 %s", updated.Status)
	}
	if updated.TotalIssues != 5 {
		t.Errorf("TotalIssues未更新，期望 5，实际 %d", updated.TotalIssues)
	}
}
