// Package cache KV 缓存抽象：Redis 实现 + 测试用内存实现。
// M3 用于默认模型配置缓存（写时失效）；不可用时静默降级为直读。
package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// KVCache 极简 KV 接口（service 层依赖此接口）。
type KVCache interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, val string, ttl time.Duration) error
	Del(ctx context.Context, key string) error
}

// NoopCache 空实现（未配置 Redis 或测试用）。
type NoopCache struct{}

func (NoopCache) Get(context.Context, string) (val string, ok bool, err error) { return "", false, nil }
func (NoopCache) Set(context.Context, string, string, time.Duration) error {
	return nil
}
func (NoopCache) Del(context.Context, string) error { return nil }

// RedisCache Redis 实现。
type RedisCache struct {
	rdb *redis.Client
}

func NewRedisCache(addr string) *RedisCache {
	return &RedisCache{rdb: redis.NewClient(&redis.Options{Addr: addr})}
}

func (c *RedisCache) Get(ctx context.Context, key string) (val string, ok bool, err error) {
	val, err = c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func (c *RedisCache) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, val, ttl).Err()
}

func (c *RedisCache) Del(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}

// Ping 连通性探测（/healthz、/readyz 用）：Redis 不可用时 asynq 队列与默认配置缓存都会失效，
// 必须能被探针发现（审计 P2-27）。
func (c *RedisCache) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *RedisCache) Close() error { return c.rdb.Close() }
