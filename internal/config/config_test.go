package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"APP_PORT", "DATABASE_URL", "REDIS_ADDR", "JWT_SECRET", "ENCRYPTION_KEY", "LOG_LEVEL", "CORS_ORIGINS"} {
		t.Setenv(k, "")
		osUnset(k)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg := Load()
	assert.Equal(t, 8080, cfg.Port)
	assert.Contains(t, cfg.DatabaseURL, "postgres://")
	assert.Equal(t, "127.0.0.1:6379", cfg.RedisAddr)
	assert.Equal(t, "info", cfg.LogLevel)
	// 未注入密钥时给出占位值而非空串
	assert.NotEmpty(t, cfg.JWTSecret)
	assert.NotEmpty(t, cfg.EncryptionKey)
}

func TestLoadPortFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("APP_PORT", "9000")
	cfg := Load()
	assert.Equal(t, 9000, cfg.Port)
}

func TestLoadInvalidPortFallsBack(t *testing.T) {
	clearEnv(t)
	t.Setenv("APP_PORT", "not-a-number")
	cfg := Load()
	assert.Equal(t, 8080, cfg.Port, "非法端口应回退默认值")
}

func TestLoadEnvFileOverride(t *testing.T) {
	// godotenv 不覆盖已存在的环境变量：显式设置应优先于 .env
	clearEnv(t)
	t.Setenv("DATABASE_URL", "postgres://explicit@127.0.0.1:5432/x?sslmode=disable")
	cfg := Load()
	assert.Contains(t, cfg.DatabaseURL, "explicit")
}
