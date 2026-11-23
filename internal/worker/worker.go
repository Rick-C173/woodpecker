package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"

	"woodpecker/internal/config"
)

// Client 封装 asynq.Client，实现 service.Enqueuer 与 Pipeline.Enqueuer（API 侧入队用）。
type Client struct{ inner *asynq.Client }

// NewClient 创建 asynq 客户端（复用 Redis）。
func NewClient(redisAddr string) *Client {
	return &Client{inner: asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})}
}

// EnqueueIngest 入队一个摄取任务（service.Enqueuer 接口实现）。
func (c *Client) EnqueueIngest(ctx context.Context, taskID int64) error {
	payload, err := json.Marshal(IngestPayload{TaskID: taskID})
	if err != nil {
		return fmt.Errorf("序列化任务载荷: %w", err)
	}
	_, err = c.inner.EnqueueContext(ctx, asynq.NewTask(TypeIngest, payload, asynq.MaxRetry(0)))
	return err
}

// EnqueueContext 透传 asynq 任务入队（Pipeline.Enqueuer 接口实现，重试重排用）。
func (c *Client) EnqueueContext(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return c.inner.EnqueueContext(ctx, task, opts...)
}

// Close 关闭客户端连接。
func (c *Client) Close() error { return c.inner.Close() }

// Server asynq 服务端（worker 模式运行）。
type Server struct {
	srv      *asynq.Server
	pipeline *Pipeline
}

// NewServer 创建 asynq 服务端并注册摄取处理器。
func NewServer(cfg *config.Config, pipeline *Pipeline) *Server {
	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr},
		asynq.Config{
			Concurrency: 4,
			Queues:      map[string]int{"default": 1},
			// 重试由 Pipeline 自管理（R3），此处关闭自动重试
		},
	)
	return &Server{srv: srv, pipeline: pipeline}
}

// Run 启动 worker（阻塞直到 ctx 取消）。
func (s *Server) Run(ctx context.Context) error {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeIngest, s.handleIngest)
	slog.Info("worker 已启动", "queue", TypeIngest, "concurrency", 4)
	if err := s.srv.Start(mux); err != nil {
		return err
	}
	<-ctx.Done()
	s.srv.Shutdown()
	return nil
}

// handleIngest asynq 处理入口：载荷只含 task_id，业务状态以库为准。
// Pipeline.Process 返回 nil（任务终结/已重排）或 error（临时故障，asynq 原样保留任务）。
func (s *Server) handleIngest(ctx context.Context, t *asynq.Task) error {
	var payload IngestPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		slog.Error("摄取任务载荷非法", "err", err)
		return nil // 永久性坏载荷：不再重投
	}
	if err := s.pipeline.Process(ctx, payload.TaskID); err != nil {
		slog.Error("摄取任务处理异常", "task_id", payload.TaskID, "err", err)
		return fmt.Errorf("摄取任务 %d: %w", payload.TaskID, err)
	}
	return nil
}
