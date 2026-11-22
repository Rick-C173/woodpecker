// Package config 从环境变量加载配置，内部工具不引配置库。
// 密钥类配置在开发环境给出占位默认值并打警告，生产必须显式注入。
package config

import (
	"log/slog"
	"os"
	"strconv"
)

type Config struct {
	Port          int
	DatabaseURL   string
	RedisAddr     string
	JWTSecret     string
	EncryptionKey string // AES-256-GCM 密钥（M3 模型凭证加密用）
	LogLevel      string
	CORSOrigins   string
}

func Load() *Config {
	cfg := &Config{
		Port:          envInt("APP_PORT", 8080),
		DatabaseURL:   envStr("DATABASE_URL", "postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker?sslmode=disable"),
		RedisAddr:     envStr("REDIS_ADDR", "127.0.0.1:6379"),
		JWTSecret:     envStr("JWT_SECRET", ""),
		EncryptionKey: envStr("ENCRYPTION_KEY", ""),
		LogLevel:      envStr("LOG_LEVEL", "info"),
		CORSOrigins:   envStr("CORS_ORIGINS", "*"),
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
