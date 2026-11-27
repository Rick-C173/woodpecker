// Woodpecker 服务入口。
// 三种运行模式：--mode=api（HTTP）、worker（摄取管道，M4 接入 asynq）、all（默认，同进程两者皆跑）。
//
//	@Title			Woodpecker API
//	@Version		1.0
//	@Description	AI 驱动的代码/技术文档知识库（RAG）。统一响应信封 {code,message,data,request_id}；错误码见 md/API设计.md §2。
//	@BasePath		/api/v1
//
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//	@description				登录后获取的 JWT：`Authorization: Bearer <token>`
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
	"woodpecker/internal/mail"
	"woodpecker/internal/oidc"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
	"woodpecker/internal/web"
	"woodpecker/internal/worker"
)

func main() {
	// TODO(方向待办-4): 形态扩展——CLI / 桌面端（独立二进制 cmd/woodpecker-cli 等），
	// 等 MCP Server 验证价值后再评估优先级。见 md/形态扩展可行性-MCP-CLI-桌面.md。（2026-11-26 规划）
	mode := flag.String("mode", "all", "运行模式：api | worker | all")
	// Windows 服务模式：sc.exe 注册后由 SCM 启动（binPath 携带 --svc）；仅 Windows 有实现。
	svcFlag := flag.Bool("svc", false, "以 Windows 服务方式运行（sc.exe 注册，仅 Windows）")
	flag.Parse()

	if *svcFlag {
		if err := runAsWindowsService(*mode); err != nil {
			fmt.Fprintf(os.Stderr, "Windows 服务模式启动失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

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

// svcCancel 服务控制管理器的取消函数：Windows 服务收到 Stop/Shutdown 时由
// svc_windows.go 调用以触发 run() 的优雅关闭；控制台模式与其他平台恒为 nil。
var svcCancel func()

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
	sigCtx, stopSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignal()
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()
	// 服务模式（Windows SCM）的 Stop 请求经 svcCancel 触发同一优雅关闭链路。
	svcCancel = cancel
	defer func() { svcCancel = nil }()

	if mode == "worker" || mode == "all" {
		go func() {
			if err := worker.Run(ctx, cfg); err != nil {
				slog.Error("worker 退出", "err", err)
				cancel()
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

	// M10：邮箱邀请制注册（FR-16）—— SMTP 配置（密码加密落 settings）+ 邀请生命周期
	inviteRepo := repo.NewInviteRepo(db)
	groupRepo := repo.NewGroupRepo(db)
	groupSvc := service.NewGroupService(groupRepo, users, kbs, auditSvc)
	oidcStore := &oidc.SettingStore{Store: repo.NewSettingRepo(db), Encrypt: cipher.Encrypt, Decrypt: cipher.Decrypt}
	oidcSvc := service.NewOIDCService(oidcStore, users, groupRepo, issuer, auditSvc)
	smtpStore := &mail.SMTPSettingStore{
		Store:   repo.NewSettingRepo(db),
		Encrypt: cipher.Encrypt,
		Decrypt: cipher.Decrypt,
	}
	smtpSender := mail.NewSMTPSender(smtpStore)
	inviteSvc := service.NewInviteService(inviteRepo, users, smtpSender, smtpStore, cfg.InviteBaseURL, auditSvc)
	authSvc.SetInvites(inviteRepo)

	// M15：个人 API Token（FR-20）——机器访问凭证（脚本/CI/CLI，MCP 前置依赖）
	tokenSvc := service.NewAPITokenService(repo.NewAPITokenRepo(db), auditSvc)

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
		Invite:        handler.NewInviteHandler(inviteSvc, authSvc, smtpStore, smtpSender),
		Groups:        handler.NewGroupHandler(groupSvc),
		OIDC:          handler.NewOIDCHandler(oidcSvc),
		Tokens:        handler.NewAPITokenHandler(tokenSvc),
		UserRepo:      users,
		Issuer:        issuer,
		KBLookup:      kbs,
		RedisPinger:   kvCache.Ping,  // /healthz、/readyz 探测 Redis（队列与默认配置缓存，P2-27）
		NoRoute:       web.Handler(), // 单二进制部署：内嵌前端 SPA（dist 未构建时返回指引页）
		OnAdminKBAccess: func(ctx context.Context, userID, kbID int64, method, path, ip string) {
			auditSvc.Record(ctx, &userID, "kb.admin_access", "kb", &kbID,
				map[string]any{"method": method, "path": path}, ip)
		},
	})
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: router,
		// 慢连接防护（P2-29）：只设头部与空闲超时——
		// ① ReadHeaderTimeout 防 Slowloris 类攻击占住连接；
		// ② IdleTimeout 回收 keep-alive 空转连接；
		// ③ ReadTimeout 必须为 0：上传 50×5MB 的请求体在慢网下可能超过任何固定值，
		//    读取超时会直接掐断上传（体积由 MaxBytesReader 兜住）；
		// ④ WriteTimeout 必须为 0：/conversations/{c}/messages 是 SSE 长连接，
		//    任何非零写超时都会在生成中途切断响应（流式输出本身可能持续数分钟）。
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
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
