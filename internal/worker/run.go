package worker

import (
	"context"
	"fmt"
	"log/slog"

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
	pipeline.SetWorkerID(cfg.WorkerID) // 审计 P1-8：重启恢复只清理本实例残留
	// 验收回归 8-1：心跳超时阈值，覆盖「WORKER_ID 跨重启变化后按归属匹配不到」的僵尸任务。
	pipeline.SetZombieTimeoutMinutes(cfg.WorkerZombieTimeoutMinutes)
	pipeline.RecoverStartup(ctx, docs)

	// 周期性僵尸回收（验收回归 8-1 补强）：覆盖「Pod 秒级重建」——崩溃后新 Pod 立刻起来时
	// 心跳还没过期，启动恢复看不到陈旧任务；由后台循环在心跳过期后的第一个周期内回收。
	// 跟随 worker 的 ctx 退出（Run 返回即停），不会泄漏。<=0 关闭并在日志中说明。
	if cfg.WorkerZombieSweepMinutes <= 0 {
		slog.Warn("WORKER_ZOMBIE_SWEEP_MINUTES<=0：周期性僵尸回收已关闭，" +
			"心跳超时的僵尸任务只能靠 worker 重启回收")
	} else {
		pipeline.SetZombieSweepMinutes(cfg.WorkerZombieSweepMinutes)
		go pipeline.RunSweeper(ctx)
	}

	return NewServer(cfg, pipeline).Run(ctx)
}
