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

	"woodpecker/internal/config"
	"woodpecker/internal/database"
	"woodpecker/internal/handler"
	"woodpecker/internal/jwt"
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *mode == "worker" || *mode == "all" {
		go func() {
			if err := worker.Run(ctx, cfg); err != nil {
				slog.Error("worker 退出", "err", err)
				stop()
			}
		}()
	}

	if *mode == "api" || *mode == "all" {
		if err := runAPI(ctx, cfg); err != nil {
			slog.Error("api 退出", "err", err)
			os.Exit(1)
		}
	}

	<-ctx.Done()
	slog.Info("进程退出")
}

func runAPI(ctx context.Context, cfg *config.Config) error {
	db, err := database.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("连接数据库: %w", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

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

	router := handler.NewRouter(handler.Deps{
		Cfg:      cfg,
		DB:       db,
		Auth:     handler.NewAuthHandler(authSvc),
		Admin:    handler.NewAdminUsersHandler(adminSvc),
		UserRepo: users,
		Issuer:   issuer,
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
