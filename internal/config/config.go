// Package config 从环境变量加载配置，内部工具不引配置库。
// 密钥类配置在开发环境给出占位默认值并打警告，生产必须显式注入。
package config

import (
	"log/slog"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port          int
	DatabaseURL   string
	RedisAddr     string
	JWTSecret     string
	EncryptionKey string // AES-256-GCM 密钥（M3 模型凭证加密用）
	LogLevel      string
	CORSOrigins   string
	JWTTLHours    int

	// 限流（API 设计 §1）：登录态全局窗口限流，req/min/用户，<=0 关闭
	RateLimitPerMin int

	// 密码策略（PRD FR-01）
	PasswordMinLen        int // 密码最小长度
	PasswordLockThreshold int // 连续失败 N 次锁定
	PasswordLockMinutes   int // 锁定时长（分钟）
	InitialPasswordLen    int // 管理员创建/重置密码的随机长度

	// 连接池（数据库设计 §6）
	DBMaxOpenConns           int
	DBMaxIdleConns           int
	DBConnMaxLifetimeMinutes int
}

// Load 读取配置：.env 文件（存在则加载，不覆盖已有环境变量）→ 环境变量 → 默认值。
func Load() *Config {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.Debug("未加载 .env", "err", err)
	}
	cfg := &Config{
		Port:          envInt("APP_PORT", 8080),
		DatabaseURL:   envStr("DATABASE_URL", "postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker?sslmode=disable"),
		RedisAddr:     envStr("REDIS_ADDR", "127.0.0.1:6379"),
		JWTSecret:     envStr("JWT_SECRET", ""),
		EncryptionKey: envStr("ENCRYPTION_KEY", ""),
		LogLevel:      envStr("LOG_LEVEL", "info"),
		CORSOrigins:   envStr("CORS_ORIGINS", "*"),
		JWTTLHours:    envInt("JWT_TTL_HOURS", 24),

		RateLimitPerMin: envInt("RATE_LIMIT_PER_MIN", 100),

		PasswordMinLen:        envInt("PASSWORD_MIN_LEN", 8),
		PasswordLockThreshold: envInt("PASSWORD_LOCK_THRESHOLD", 5),
		PasswordLockMinutes:   envInt("PASSWORD_LOCK_MINUTES", 10),
		InitialPasswordLen:    envInt("INITIAL_PASSWORD_LEN", 12),

		DBMaxOpenConns:           envInt("DB_MAX_OPEN_CONNS", 20),
		DBMaxIdleConns:           envInt("DB_MAX_IDLE_CONNS", 5),
		DBConnMaxLifetimeMinutes: envInt("DB_CONN_MAX_LIFETIME_MINUTES", 30),
	}
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "dev-only-jwt-secret"
		slog.Warn("JWT_SECRET 未设置，使用开发占位密钥；生产环境必须显式注入")
	}
	if cfg.EncryptionKey == "" {
		cfg.EncryptionKey = "dev-only-encryption-key-32bytes!!"
		slog.Warn("ENCRYPTION_KEY 未设置，使用开发占位密钥；生产环境必须显式注入（M3 起 AES-256-GCM 依赖）")
	}
	return cfg
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		slog.Warn("环境变量非整数，使用默认值", "key", key, "value", v, "default", def)
	}
	return def
}
