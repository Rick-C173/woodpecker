package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"APP_ENV", "APP_PORT", "DATABASE_URL", "REDIS_ADDR", "JWT_SECRET", "ENCRYPTION_KEY", "LOG_LEVEL", "CORS_ORIGINS",
		"RATE_LIMIT_PER_MIN", "PASSWORD_MIN_LEN", "PASSWORD_LOCK_THRESHOLD", "PASSWORD_LOCK_MINUTES", "INITIAL_PASSWORD_LEN",
		"DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS", "DB_CONN_MAX_LIFETIME_MINUTES",
		"BANNER_ENABLED", "LOG_TO_FILE", "LOG_FILE_PATH", "UPLOAD_DIR"} {
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
	// 限流 / 密码策略 / 连接池默认值
	assert.Equal(t, 100, cfg.RateLimitPerMin)
	assert.Equal(t, 8, cfg.PasswordMinLen)
	assert.Equal(t, 5, cfg.PasswordLockThreshold)
	assert.Equal(t, 10, cfg.PasswordLockMinutes)
	assert.Equal(t, 12, cfg.InitialPasswordLen)
	assert.Equal(t, 20, cfg.DBMaxOpenConns)
	assert.Equal(t, 5, cfg.DBMaxIdleConns)
	assert.Equal(t, 30, cfg.DBConnMaxLifetimeMinutes)
	// banner / 日志文件 / 上传目录默认值
	assert.True(t, cfg.BannerEnabled, "banner 缺省开启")
	assert.False(t, cfg.LogToFile, "日志写文件缺省关闭")
	assert.Equal(t, "logs/app.log", cfg.LogFilePath)
	assert.Equal(t, "data/uploads", cfg.UploadDir)
}

func TestLoadPolicyFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("RATE_LIMIT_PER_MIN", "0") // 0 = 关闭限流
	t.Setenv("PASSWORD_MIN_LEN", "12")
	t.Setenv("PASSWORD_LOCK_THRESHOLD", "3")
	t.Setenv("PASSWORD_LOCK_MINUTES", "15")
	t.Setenv("INITIAL_PASSWORD_LEN", "16")
	t.Setenv("DB_MAX_OPEN_CONNS", "40")
	t.Setenv("DB_CONN_MAX_LIFETIME_MINUTES", "60")
	cfg := Load()
	assert.Equal(t, 0, cfg.RateLimitPerMin)
	assert.Equal(t, 12, cfg.PasswordMinLen)
	assert.Equal(t, 3, cfg.PasswordLockThreshold)
	assert.Equal(t, 15, cfg.PasswordLockMinutes)
	assert.Equal(t, 16, cfg.InitialPasswordLen)
	assert.Equal(t, 40, cfg.DBMaxOpenConns)
	assert.Equal(t, 60, cfg.DBConnMaxLifetimeMinutes)
}

func TestLoadInvalidPolicyFallsBack(t *testing.T) {
	clearEnv(t)
	t.Setenv("RATE_LIMIT_PER_MIN", "abc")
	t.Setenv("PASSWORD_MIN_LEN", "xyz")
	cfg := Load()
	assert.Equal(t, 100, cfg.RateLimitPerMin, "非法数值应回退默认值")
	assert.Equal(t, 8, cfg.PasswordMinLen)
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

func TestLoadBannerAndLogSwitches(t *testing.T) {
	clearEnv(t)
	t.Setenv("BANNER_ENABLED", "false")
	t.Setenv("LOG_TO_FILE", "true")
	t.Setenv("LOG_FILE_PATH", "var/log/wp.log")
	cfg := Load()
	assert.False(t, cfg.BannerEnabled)
	assert.True(t, cfg.LogToFile)
	assert.Equal(t, "var/log/wp.log", cfg.LogFilePath)

	// 非法布尔值回退默认
	clearEnv(t)
	t.Setenv("BANNER_ENABLED", "not-bool")
	t.Setenv("LOG_TO_FILE", "yes!")
	cfg = Load()
	assert.True(t, cfg.BannerEnabled, "非法布尔应回退默认（开）")
	assert.False(t, cfg.LogToFile, "非法布尔应回退默认（关）")
}

// TestEnvDefaultsAndIsProduction 运行环境读取与判定（审计 P1-5）。
func TestEnvDefaultsAndIsProduction(t *testing.T) {
	clearEnv(t)
	cfg := Load()
	assert.Equal(t, EnvDevelopment, cfg.Env, "APP_ENV 缺省 development")
	assert.False(t, cfg.IsProduction())
	require.NoError(t, cfg.Validate(), "开发环境不做密钥强校验")

	for _, env := range []string{"production", "PROD", " production "} {
		clearEnv(t)
		t.Setenv("APP_ENV", env)
		cfg = Load()
		assert.True(t, cfg.IsProduction(), "APP_ENV=%q 应判定为生产", env)
	}

	clearEnv(t)
	t.Setenv("APP_ENV", "staging")
	assert.False(t, Load().IsProduction(), "非 production/prod 不算生产")
}

// TestValidateProductionRequiresSecrets 生产缺密钥必须拒绝启动（修复前仅 Warn 后继续跑）。
func TestValidateProductionRequiresSecrets(t *testing.T) {
	cases := []struct {
		name string
		jwt  string
		enc  string
		want string
	}{
		{name: "两者皆空", jwt: "", enc: "", want: "JWT_SECRET"},
		{name: "缺加密密钥", jwt: "a-strong-jwt-secret-value-32bytes!", enc: "", want: "ENCRYPTION_KEY"},
		{name: "缺签名密钥", jwt: "", enc: "0123456789abcdef0123456789abcdef", want: "JWT_SECRET"},
		{name: "加密密钥过弱", jwt: "a-strong-jwt-secret-value-32bytes!", enc: "short-key", want: "不得短于"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("APP_ENV", "production")
			cfg := Load()
			// Load 生产环境不回落占位密钥
			cfg.JWTSecret, cfg.EncryptionKey = tc.jwt, tc.enc
			err := cfg.Validate()
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	// 合规配置：通过
	clearEnv(t)
	t.Setenv("APP_ENV", "production")
	cfg := Load()
	cfg.JWTSecret = "a-strong-jwt-secret-value-32bytes!"
	cfg.EncryptionKey = "0123456789abcdef0123456789abcdef"
	require.NoError(t, cfg.Validate())
}

// TestLoadProductionDoesNotFallbackSecrets 生产环境不得回落硬编码占位密钥。
func TestLoadProductionDoesNotFallbackSecrets(t *testing.T) {
	clearEnv(t)
	t.Setenv("APP_ENV", "production")
	cfg := Load()
	assert.Empty(t, cfg.JWTSecret, "生产缺失 JWT_SECRET 不得回落占位值")
	assert.Empty(t, cfg.EncryptionKey, "生产缺失 ENCRYPTION_KEY 不得回落占位值")
	require.Error(t, cfg.Validate(), "生产缺密钥必须在启动校验阶段失败")

	// 开发环境保留占位密钥（便于本地起服务）
	clearEnv(t)
	dev := Load()
	assert.Equal(t, devJWTSecret, dev.JWTSecret)
	assert.Equal(t, devEncryptionKey, dev.EncryptionKey)
}
