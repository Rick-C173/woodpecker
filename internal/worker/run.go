package worker

import (
	"context"
	"fmt"

	"woodpecker/internal/cache"
	"woodpecker/internal/config"
	"woodpecker/internal/crypto"
	"woodpecker/internal/database"
	"woodpecker/internal/llm"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
)

// Run 独立 worker 模式入口（--mode=worker|all）：自建连接与依赖后阻塞运行。
func Run(ctx context.Context, cfg *config.Config) error {
	db, err := database.Open(cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns, cfg.DBConnMaxLifetimeMinutes)
	if err != nil {
		return fmt.Errorf("worker 连接数据库: %w", err)
	}
	sqlDB, _ := db.DB()
	defer func() { _ = sqlDB.Close() }()

	if err := database.Migrate(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("worker 执行迁移: %w", err)
	}

	cipher, err := crypto.NewAESGCM(cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("worker 初始化加密组件: %w", err)
	}
	auditSvc := service.NewAuditService(repo.NewAuditRepo(db))
	settings := repo.NewSettingRepo(db)
	modelCfgSvc := service.NewModelConfigService(repo.NewModelCfgRepo(db), cipher, cache.NewRedisCache(cfg.RedisAddr), llm.NewOpenAIProvider(), auditSvc)

	docs := repo.NewDocumentRepo(db)
	queue := NewClient(cfg.RedisAddr)
	defer func() { _ = queue.Close() }()

	pipeline := NewPipeline(docs, settings, cipher, modelCfgSvc, llm.NewOpenAIProvider(), queue)
	pipeline.SetUploadDir(cfg.UploadDir)
	pipeline.RecoverStartup(ctx, docs)

	return NewServer(cfg, pipeline).Run(ctx)
}
