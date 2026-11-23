// Woodpecker 服务入口。
// 三种运行模式：--mode=api（HTTP）、worker（摄取管道，M4 接入 asynq）、all（默认，同进程两者皆跑）。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"woodpecker/internal/cache"
	"woodpecker/internal/config"
	"woodpecker/internal/crypto"
	"woodpecker/internal/database"
	"woodpecker/internal/handler"
	"woodpecker/internal/jwt"
	"woodpecker/internal/llm"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
	"woodpecker/internal/worker"
)

func main() {
	mode := flag.String("mode", "all", "运行模式：api | worker | all")
	flag.Parse()

	cfg := config.Load()

	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))

	switch *mode {
	case "api", "worker", "all":
	default:
		fmt.Fprintf(os.Stderr, "无效的 --mode=%q，可选值：api | worker | all\n", *mode)
		os.Exit(2)
	}

	os.Exit(run(*mode, cfg))
}

// run 服务主流程；返回进程退出码（0=正常，1=异常）。
func run(mode string, cfg *config.Config) int {
	slog.Info("服务启动", "mode", mode, "port", cfg.Port)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if mode == "worker" || mode == "all" {
		go func() {
			if err := worker.Run(ctx, cfg); err != nil {
				slog.Error("worker 退出", "err", err)
				stop()
			}
		}()
	}

	if mode == "api" || mode == "all" {
		if err := runAPI(ctx, cfg); err != nil {
			slog.Error("api 退出", "err", err)
			return 1
		}
	}

	<-ctx.Done()
	slog.Info("进程退出")
	return 0
}

func runAPI(ctx context.Context, cfg *config.Config) error {
	db, err := database.Open(cfg.DatabaseURL, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns, cfg.DBConnMaxLifetimeMinutes)
	if err != nil {
		return fmt.Errorf("连接数据库: %w", err)
	}
	sqlDB, _ := db.DB()
	defer func() { _ = sqlDB.Close() }()

	if err := database.Migrate(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("执行迁移: %w", err)
	}

	// 组合根：repo → service → handler（技术选型 §5 分层）
	users := repo.NewUserRepo(db)
	auditSvc := service.NewAuditService(repo.NewAuditRepo(db))
	settings := repo.NewSettingRepo(db)
	issuer := jwt.NewIssuer(cfg.JWTSecret, time.Duration(cfg.JWTTLHours)*time.Hour)
	authSvc := service.NewAuthService(users, settings, auditSvc, issuer)
	adminSvc := service.NewUserAdminService(users, auditSvc)
	kbs := repo.NewKBRepo(db)
	kbSvc := service.NewKBService(kbs, auditSvc)
	// 密码策略来自配置文件（PRD FR-01）
	policy := service.PasswordPolicy{
		MinLen:        cfg.PasswordMinLen,
		LockThreshold: cfg.PasswordLockThreshold,
		LockDuration:  time.Duration(cfg.PasswordLockMinutes) * time.Minute,
		InitialLen:    cfg.InitialPasswordLen,
	}
	authSvc.SetPolicy(policy)
	adminSvc.SetPolicy(policy)

	// M3：模型服务配置（FR-12）—— AES-GCM 加密 + Redis 默认配置缓存 + OpenAI 兼容 Provider
	cipher, err := crypto.NewAESGCM(cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("初始化加密组件: %w", err)
	}
	modelCfgRepo := repo.NewModelCfgRepo(db)
	provider := llm.NewOpenAIProvider()
	kvCache := cache.NewRedisCache(cfg.RedisAddr)
	defer func() { _ = kvCache.Close() }()
	modelCfgSvc := service.NewModelConfigService(modelCfgRepo, cipher, kvCache, provider, auditSvc)

	router := handler.NewRouter(handler.Deps{
		Cfg:      cfg,
		DB:       db,
		Auth:     handler.NewAuthHandler(authSvc),
		Admin:    handler.NewAdminUsersHandler(adminSvc),
		KB:       handler.NewKBHandler(kbSvc),
		ModelCfg: handler.NewModelCfgHandler(modelCfgSvc),
		UserRepo: users,
		Issuer:   issuer,
		KBLookup: kbs,
		OnAdminKBAccess: func(ctx context.Context, userID, kbID int64, method, path, ip string) {
			auditSvc.Record(ctx, &userID, "kb.admin_access", "kb", &kbID,
				map[string]any{"method": method, "path": path}, ip)
		},
	})
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: router,
	}

	go func() {
		slog.Info("api 已启动", "addr", srv.Addr, "mode", "api")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http 服务异常", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("正在优雅关闭 api...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
