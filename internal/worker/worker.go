// Package worker 承载摄取管道后台任务。
// M0 为存根：验证 --mode=worker 进程生命周期；asynq 队列与 parse/chunk/embed/index 任务在 M4 接入。
package worker

import (
	"context"
	"log/slog"

	"woodpecker/internal/config"
)

func Run(ctx context.Context, cfg *config.Config) error {
	slog.Info("worker 启动（存根）", "redis", cfg.RedisAddr, "note", "asynq 任务接入见 M4")
	<-ctx.Done()
	slog.Info("worker 已停止")
	return nil
}
