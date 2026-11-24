// Woodpecker 服务入口。
// 三种运行模式：--mode=api（HTTP）、worker（摄取管道，M4 接入 asynq）、all（默认，同进程两者皆跑）。
//
//	@Title		Woodpecker API
//	@Version	1.0
//	@Description	AI 驱动的代码/技术文档知识库（RAG）。统一响应信封 {code,message,data,request_id}；错误码见 md/API设计.md §2。
//	@BasePath	/api/v1
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	// 生产环境缺密钥/弱密钥直接拒绝启动（审计 P1-5），不做「Warn 后继续」。
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "配置校验失败，拒绝启动: %v\n", err)
		os.Exit(1)
	}

	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	logWriter := setupLogWriter(cfg)
	slog.SetDefault(slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: logLevel})))

	printBanner(cfg)

	switch *mode {
	case "api", "worker", "all":
	default:
		fmt.Fprintf(os.Stderr, "无效的 --mode=%q，可选值：api | worker | all\n", *mode)
		os.Exit(2)
	}

	os.Exit(run(*mode, cfg))
}

// setupLogWriter 组装日志输出：LOG_TO_FILE=true 时控制台与文件双写（目录自动创建）；
// 文件打开失败降级为仅控制台并告警。
func setupLogWriter(cfg *config.Config) io.Writer {
	out := io.Writer(os.Stdout)
	if !cfg.LogToFile {
		return out
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LogFilePath), 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "创建日志目录失败（降级为仅控制台）: %v\n", err)
		return out
	}
	f, err := os.OpenFile(cfg.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开日志文件失败（降级为仅控制台）: %v\n", err)
		return out
	}
	// 进程退出前冲刷关闭（run 返回后由 main os.Exit 结束）
	closer = f
	return io.MultiWriter(os.Stdout, f)
}

// closer 日志文件句柄（LOG_TO_FILE=true 时非空），进程退出前关闭。
var closer io.Closer

// maskDSN 脱敏数据源连接串（密码替换为 ***）。
func maskDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	// url.String() 会把 *** 编码为 %2A%2A%2A，还原为可读形式
	return strings.ReplaceAll(u.String(), "%2A%2A%2A", "***")
}

// logFileForSummary 配置摘要里的日志文件展示（未开启时显示关闭态）。
func logFileForSummary(cfg *config.Config) string {
	if cfg.LogToFile {
		return cfg.LogFilePath
	}
	return "（关闭）"
}

// run 服务主流程；返回进程退出码（0=正常，1=异常）。
func run(mode string, cfg *config.Config) int {
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	slog.Info("服务启动", "mode", mode, "port", cfg.Port, "env", cfg.Env)
	slog.Info("配置摘要",
		"db", maskDSN(cfg.DatabaseURL),
		"redis", cfg.RedisAddr,
		"upload_dir", cfg.UploadDir,
		"log_file", logFileForSummary(cfg),
		"rate_limit_per_min", cfg.RateLimitPerMin,
	)
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

	// M4：文档与摄取（FR-03/04/05/11）—— asynq 入队 + Document API
	docQueue := worker.NewClient(cfg.RedisAddr)
	defer func() { _ = docQueue.Close() }()
	docSvc := service.NewDocumentService(repo.NewDocumentRepo(db), settings, cipher, docQueue, auditSvc)
	docSvc.SetUploadDir(cfg.UploadDir)

	// M5：混合检索（FR-10）—— 查询向量化 + RRF 融合 + rerank 精排
	searchSvc := service.NewSearchService(repo.NewSearchRepo(db), modelCfgSvc, provider, provider)

	// M6：对话问答（FR-07/08/09/14）—— SSE 流式问答 + 会话管理 + 引用落库 + 点踩反馈
	chatSvc := service.NewChatService(repo.NewConversationRepo(db), kbs, modelCfgSvc, provider, searchSvc)

	// M7：系统设置与审计/反馈查询（FR-13 / FR-14 / FR-15）
	settingsSvc := service.NewSettingsService(repo.NewSettingRepo(db), auditSvc)
	adminQuerySvc := service.NewAdminQueryService(repo.NewAuditRepo(db), repo.NewConversationRepo(db))

	router := handler.NewRouter(handler.Deps{
		Cfg:           cfg,
		DB:            db,
		Auth:          handler.NewAuthHandler(authSvc),
		Admin:         handler.NewAdminUsersHandler(adminSvc),
		KB:            handler.NewKBHandler(kbSvc),
		ModelCfg:      handler.NewModelCfgHandler(modelCfgSvc),
		Document:      handler.NewDocumentHandler(docSvc),
		Search:        handler.NewSearchHandler(searchSvc),
		Chat:          handler.NewChatHandler(chatSvc),
		AdminSettings: handler.NewAdminSettingsHandler(settingsSvc, adminQuerySvc),
		UserRepo:      users,
		Issuer:        issuer,
		KBLookup:      kbs,
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
