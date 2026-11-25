// Package config 从环境变量加载配置，内部工具不引配置库。
// 密钥类配置仅在**非生产**环境给出占位默认值并打警告；生产（APP_ENV=production）
// 缺失或过弱时由 Validate 返回错误，由 cmd/server/main.go 拒绝启动（审计 P1-5）。
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// 环境名（APP_ENV）取值。
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
	EnvProdAlias   = "prod"
)

// MinEncryptionKeyLen ENCRYPTION_KEY 最小长度（字节）：
// AES-256-GCM 密钥派生不允许弱密钥，过短等同可穷举（审计 P1-5）。
const MinEncryptionKeyLen = 16

// RecommendedJWTSecretLen 生产 JWT_SECRET 推荐长度（字节）；不足时告警但不拒绝启动。
const RecommendedJWTSecretLen = 32

// DefaultWorkerZombieTimeoutMinutes 僵尸任务心跳超时阈值缺省值（分钟，验收回归 8-1）。
// worker 心跳间隔为 30s（worker 包常量），5 分钟 = 心跳间隔的 10 倍，留足两次以上容错。
const DefaultWorkerZombieTimeoutMinutes = 5

// DefaultWorkerZombieSweepMinutes 周期性僵尸回收间隔缺省值（分钟，验收回归 8-1 补强）。
// <=0 关闭 sweeper；2 分钟 = 4 个心跳周期（心跳 30s），用于覆盖「Pod 秒级重建」时
// 启动恢复看不到陈旧心跳的场景。
const DefaultWorkerZombieSweepMinutes = 2

// 开发环境占位密钥（仅非生产可回落，生产缺失直接拒绝启动）。
const (
	devJWTSecret     = "dev-only-jwt-secret"
	devEncryptionKey = "dev-only-encryption-key-32bytes!!"
)

type Config struct {
	// Env 运行环境（APP_ENV）：development（默认）/ production（别名 prod）
	Env           string
	Port          int
	DatabaseURL   string
	RedisAddr     string
	JWTSecret     string
	EncryptionKey string // AES-256-GCM 密钥（M3 模型凭证加密用）
	LogLevel      string
	CORSOrigins   string
	JWTTLHours    int

	// 启动展示（PRD FR-00 体验项）
	BannerEnabled bool // 启动 banner 开关（BANNER_ENABLED，缺省开）

	// 日志输出
	LogToFile   bool   // 日志写文件开关（LOG_TO_FILE，缺省关=仅控制台）
	LogFilePath string // 日志文件路径（LOG_FILE_PATH，append 追加；开=true 时与控制台双写）

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

	// 摄取（M4）：上传文件落地目录
	UploadDir string
	// InviteBaseURL 站点外部访问地址（FR-16 邀请链接前缀，如 https://wp.corp.com）。
	// 部署属性，独立于 SMTP 配置；为空时邀请降级链接为空（仅邮件路径可用）。
	InviteBaseURL string

	// WorkerID 摄取 worker 标识（审计 P1-8）。任务开始处理时写入 ingest_tasks.worker_id，
	// 重启恢复只清理「本标识」的残留任务。多实例部署（含同主机多容器）**必须各自配置唯一值**，
	// 缺省取主机名：同主机多容器会得到相同的值，退化为互相清理对方任务。
	WorkerID string

	// WorkerZombieTimeoutMinutes 僵尸任务的心跳超时阈值（分钟，验收回归 8-1）。
	// 运行中任务超过该时长未刷新 heartbeat_at 即被视为孤儿任务并置失败，
	// 用于覆盖「WORKER_ID 跨重启变化 → 按归属匹配不到」的场景（k8s 重建 Pod 是常态）。
	// 必须显著大于 worker 心跳间隔（默认 30s，建议 ≥5 倍），否则正常任务会被误杀。
	WorkerZombieTimeoutMinutes int

	// WorkerZombieSweepMinutes 周期性僵尸回收间隔（分钟，验收回归 8-1 补强）。
	// 心跳超时的运行中任务由后台循环定期置失败，覆盖「Pod 秒级重建 → 启动恢复时心跳还没过期」
	// 的场景（否则文档只能等下一次重启才被回收）。<=0 关闭 sweeper。
	WorkerZombieSweepMinutes int
}

// Load 读取配置：.env 文件（存在则加载，不覆盖已有环境变量）→ 环境变量 → 默认值。
func Load() *Config {
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.Debug("未加载 .env", "err", err)
	}
	cfg := &Config{
		Env:           envStr("APP_ENV", EnvDevelopment),
		Port:          envInt("APP_PORT", 8080),
		DatabaseURL:   envStr("DATABASE_URL", "postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker?sslmode=disable"),
		RedisAddr:     envStr("REDIS_ADDR", "127.0.0.1:6379"),
		JWTSecret:     envStr("JWT_SECRET", ""),
		EncryptionKey: envStr("ENCRYPTION_KEY", ""),
		LogLevel:      envStr("LOG_LEVEL", "info"),
		CORSOrigins:   envStr("CORS_ORIGINS", "*"),
		JWTTLHours:    envInt("JWT_TTL_HOURS", 24),

		BannerEnabled: envBool("BANNER_ENABLED", true),
		LogToFile:     envBool("LOG_TO_FILE", false),
		LogFilePath:   envStr("LOG_FILE_PATH", "logs/app.log"),

		RateLimitPerMin: envInt("RATE_LIMIT_PER_MIN", 100),

		PasswordMinLen:        envInt("PASSWORD_MIN_LEN", 8),
		PasswordLockThreshold: envInt("PASSWORD_LOCK_THRESHOLD", 5),
		PasswordLockMinutes:   envInt("PASSWORD_LOCK_MINUTES", 10),
		InitialPasswordLen:    envInt("INITIAL_PASSWORD_LEN", 12),

		DBMaxOpenConns:           envInt("DB_MAX_OPEN_CONNS", 20),
		DBMaxIdleConns:           envInt("DB_MAX_IDLE_CONNS", 5),
		DBConnMaxLifetimeMinutes: envInt("DB_CONN_MAX_LIFETIME_MINUTES", 30),

		UploadDir: envStr("UPLOAD_DIR", "data/uploads"),
		InviteBaseURL: envStr("APP_BASE_URL", ""),

		WorkerID: envStr("WORKER_ID", DefaultWorkerID()),

		WorkerZombieTimeoutMinutes: envInt("WORKER_ZOMBIE_TIMEOUT_MINUTES", DefaultWorkerZombieTimeoutMinutes),
		WorkerZombieSweepMinutes:   envInt("WORKER_ZOMBIE_SWEEP_MINUTES", DefaultWorkerZombieSweepMinutes),
	}
	if cfg.JWTSecret == "" {
		if cfg.IsProduction() {
			// 生产缺失：不回落占位密钥，留空交由 Validate 拒绝启动（审计 P1-5）。
			slog.Error("生产环境未设置 JWT_SECRET，启动将被拒绝")
		} else {
			cfg.JWTSecret = devJWTSecret
			slog.Warn("JWT_SECRET 未设置，使用开发占位密钥；生产环境必须显式注入")
		}
	} else if cfg.IsProduction() && len(cfg.JWTSecret) < RecommendedJWTSecretLen {
		slog.Warn("JWT_SECRET 长度偏短，建议至少 32 字节", "len", len(cfg.JWTSecret))
	}
	if cfg.EncryptionKey == "" {
		if cfg.IsProduction() {
			// 同上：生产缺失不回落占位密钥（否则库泄露即等同明文，审计 P1-5）。
			slog.Error("生产环境未设置 ENCRYPTION_KEY，启动将被拒绝")
		} else {
			cfg.EncryptionKey = devEncryptionKey
			slog.Warn("ENCRYPTION_KEY 未设置，使用开发占位密钥；生产环境必须显式注入（M3 起 AES-256-GCM 依赖）")
		}
	}
	return cfg
}

// IsProduction 生产环境判定（APP_ENV=production，别名 prod；大小写不敏感）。
func (c *Config) IsProduction() bool {
	switch strings.ToLower(strings.TrimSpace(c.Env)) {
	case EnvProduction, EnvProdAlias:
		return true
	}
	return false
}

// Validate 启动前安全校验（审计 P1-5）：生产缺密钥或密钥过弱必须拒绝启动，
// 不做「打条 Warn 继续跑」。开发环境不做任何限制（保留占位密钥，便于本地起服务）。
//
// 由 cmd/server/main.go 调用后 os.Exit(1)，保持本函数可测（不在此处退出进程）。
func (c *Config) Validate() error {
	if !c.IsProduction() {
		return nil
	}
	if strings.TrimSpace(c.JWTSecret) == "" {
		return errors.New("APP_ENV=production 时必须显式注入 JWT_SECRET")
	}
	if strings.TrimSpace(c.EncryptionKey) == "" {
		return errors.New("APP_ENV=production 时必须显式注入 ENCRYPTION_KEY")
	}
	if len(c.EncryptionKey) < MinEncryptionKeyLen {
		return fmt.Errorf("ENCRYPTION_KEY 长度为 %d 字节，生产环境不得短于 %d 字节（弱密钥）",
			len(c.EncryptionKey), MinEncryptionKeyLen)
	}
	return nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// DefaultWorkerID 缺省 worker 标识：主机名（审计 P1-8）。worker 包组装 Pipeline 时复用本函数，
// 保证「缺省值」只有一处定义。
// 取不到主机名时回落固定值并告警——不可留空，否则重启恢复会退化为清理 worker_id IS NULL 的分支，
// 把所有「刚入队尚未被消费」的任务一并置失败。
func DefaultWorkerID() string {
	if h, err := os.Hostname(); err == nil {
		if h = strings.TrimSpace(h); h != "" {
			return h
		}
	}
	slog.Warn("无法获取主机名，WORKER_ID 回落为 worker-unknown；多实例部署必须显式配置 WORKER_ID")
	return "worker-unknown"
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

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
		slog.Warn("环境变量非布尔值，使用默认值", "key", key, "value", v, "default", def)
	}
	return def
}
