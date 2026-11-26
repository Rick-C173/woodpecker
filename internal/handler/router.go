package handler

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"woodpecker/internal/config"
	"woodpecker/internal/jwt"
	"woodpecker/internal/middleware"
	"woodpecker/internal/model"
)

// UserLookupRepo Auth 中间件所需的用户状态查询（*repo.UserRepo 满足该接口）。
type UserLookupRepo interface {
	GetByID(ctx context.Context, id int64) (*model.User, error)
}

// Deps 组合根注入：main.go 构造 repos/services 后传入（技术选型 §5 分层）。
type Deps struct {
	Cfg      *config.Config
	DB       *gorm.DB
	Auth     *AuthHandler
	Admin    *AdminUsersHandler
	KB       *KBHandler
	UserRepo UserLookupRepo
	Issuer   *jwt.Issuer

	// M3：模型服务配置（FR-12）
	ModelCfg *ModelCfgHandler

	// M4：文档与摄取（FR-03/04/05/11）
	Document *DocumentHandler

	// M5：混合检索（FR-10）
	Search *SearchHandler

	// M6：会话与问答（FR-07/08/09/14）
	Chat *ChatHandler

	// M7：系统设置与审计/反馈查询（FR-13 / FR-14 / FR-15）
	AdminSettings *AdminSettingsHandler

	// M10：邮箱邀请制注册（FR-16）
	Invite *InviteHandler

	// M12：用户组与组授权（FR-18）
	Groups *GroupHandler

	// M13：企业单点登录 SSO / OIDC（FR-19）
	OIDC *OIDCHandler

	KBLookup middleware.KBLookup
	// OnAdminKBAccess 管理员以非成员身份访问库时回调（审计）
	OnAdminKBAccess func(ctx context.Context, userID, kbID int64, method, path, ip string)

	Pinger Pinger // 可选：测试注入探针；为空时由 DB 派生
	// RedisPinger 可选：Redis（asynq 队列/默认配置缓存）探针；为空时探针视为未配置该依赖（P2-27）
	RedisPinger Pinger

	// NoRoute 可选：非 API 路径兜底处理器——单二进制部署时内嵌前端 SPA
	// （internal/web.Handler()，见 scripts/deploy 裸机方案）；为空保持 gin 默认 404。
	NoRoute gin.HandlerFunc
}

// NewRouter 装配全局中间件与 /api/v1 路由。
// 后续里程碑在此追加：kbs/documents(M2/M4) → search(M5) → conversations(M6) → admin-settings/audit(M7)。
func NewRouter(d Deps) *gin.Engine {
	cfg := d.Cfg
	if cfg.LogLevel != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(
		middleware.RequestID(),
		middleware.Logger(),
		middleware.CORS(cfg.CORSOrigins),
		gin.Recovery(),
	)

	var pinger Pinger
	if d.Pinger != nil {
		pinger = d.Pinger
	} else {
		sqlDB, err := d.DB.DB()
		if err != nil {
			panic(err)
		}
		pinger = func(ctx context.Context) error { return sqlDB.PingContext(ctx) }
	}

	v1 := r.Group("/api/v1")
	v1.GET("/healthz", Health()) // liveness：仅进程存活，不探测依赖
	v1.GET("/readyz", Readyz(pinger, d.RedisPinger))

	// auth（公开）
	v1.POST("/auth/login", d.Auth.Login)
	v1.POST("/auth/register", d.Auth.Register)
	// M10：邮箱邀请制（FR-16）公开端点
	if d.Invite != nil {
		v1.GET("/auth/invitations/:token", d.Invite.ValidateInvitation)
		v1.POST("/auth/register/invited", d.Invite.RegisterInvited)
	}

	// M13：SSO / OIDC（FR-19，公开；未启用时 login 404、前端按钮隐藏）
	if d.OIDC != nil {
		v1.GET("/auth/oidc/status", d.OIDC.Status)
		v1.GET("/auth/oidc/login", d.OIDC.Login)
		v1.GET("/auth/oidc/callback", d.OIDC.Callback)
		v1.POST("/auth/oidc/token", d.OIDC.TokenExchange)
	}

	// auth（登录态）：全局限流（API 设计 §1，阈值可配 RATE_LIMIT_PER_MIN，<=0 关闭）
	limiter := middleware.NewRateLimiter(cfg.RateLimitPerMin, time.Minute)
	authed := v1.Group("", middleware.Auth(d.Issuer, userLookup(d.UserRepo)), limiter.Handler(middleware.UserKey))
	// 改密接口本身（含首登强制改密）不受强制改密拦截；成功后 token_version+1 需重新登录。
	authed.POST("/auth/password", d.Auth.ChangePassword)

	// 首登强制改密（PRD FR-01 / 审计 P1-4）：除改密白名单外，带 must_change_password 的会话一律 403 AUTH_07。
	// mustChangeOK 与 authed 权限完全一致，只是多一层状态拦截。
	mustChangeOK := authed.Group("", middleware.RequirePasswordChanged())

	// 知识库（FR-02）：建/改/删与成员授权仅管理员；库内读取需成员资格（R6 不可见原则）
	var adminBypass func(*gin.Context, int64, int64)
	if d.OnAdminKBAccess != nil {
		adminBypass = func(c *gin.Context, userID, kbID int64) {
			d.OnAdminKBAccess(c.Request.Context(), userID, kbID,
				c.Request.Method, c.Request.URL.Path, c.ClientIP())
		}
	}
	kbs := mustChangeOK.Group("/kbs")
	kbs.GET("", d.KB.List)
	kbs.POST("", middleware.Admin(), d.KB.Create)
	kb := kbs.Group("/:kb", middleware.KBAccess(d.KBLookup, adminBypass))
	kb.GET("", d.KB.Get)
	kb.PATCH("", middleware.Admin(), d.KB.Update)
	kb.DELETE("", middleware.Admin(), d.KB.Delete)
	// 成员管理（R6）：管理员或库内管理者（owner 可授权，FR-02 扩展）
	members := kb.Group("/members", middleware.KBMemberManager())
	members.GET("", d.KB.ListMembers)
	members.POST("", d.KB.AddMember)
	members.PATCH("/:user", d.KB.UpdateMemberRole)
	members.DELETE("/:user", d.KB.RemoveMember)

	// 组授权（FR-18）：管理员或库内管理者
	if d.Groups != nil {
		gr := kb.Group("/group-roles", middleware.KBMemberManager())
		gr.GET("", d.Groups.ListGrants)
		gr.POST("", d.Groups.Grant)
		gr.PATCH("/:group", d.Groups.UpdateGrant)
		gr.DELETE("/:group", d.Groups.RevokeGrant)
	}

	// 检索（FR-10，API设计.md §3.7）：成员可用（KBAccess 已拦截）
	if d.Search != nil {
		kb.GET("/search", d.Search.Search)
	}

	// 会话（FR-09，API设计.md §3.7）：库内建/列（成员），详情/问答在顶层 /conversations 组
	if d.Chat != nil {
		kb.GET("/conversations", d.Chat.ListConversations)
		kb.POST("/conversations", d.Chat.CreateConversation)
	}

	// 文档与摄取（FR-03/04/05/11，API设计.md §3.6）
	// 读取=成员（KBAccess 已拦截）；接入/重试/同步=编辑者+；删除/重建索引=管理者+（R6）
	if d.Document != nil {
		docs := kb.Group("/documents")
		docs.GET("", d.Document.List)
		docs.GET("/:doc", d.Document.Get)
		docs.POST("/git", middleware.RequireKBEditor(), d.Document.CreateGit)
		docs.POST("/upload", middleware.RequireKBEditor(), d.Document.CreateUpload)
		docs.POST("/url", middleware.RequireKBEditor(), d.Document.CreateURL)
		docs.POST("/:doc/retry", middleware.RequireKBEditor(), d.Document.Retry)
		docs.POST("/:doc/sync", middleware.RequireKBEditor(), d.Document.Sync)
		docs.DELETE("/:doc", middleware.RequireKBOwner(), d.Document.Delete)
		docs.POST("/:doc/reindex", middleware.RequireKBOwner(), d.Document.Reindex)
		// M14 Git 自动同步：定时拉取开关 + webhook 验签 token（编辑者+）
		docs.PATCH("/:doc/auto-sync", middleware.RequireKBEditor(), d.Document.SetAutoSync)
		docs.POST("/:doc/webhook-secret", middleware.RequireKBEditor(), d.Document.RotateWebhookSecret)
		// M14 webhook 回调：公开路由（Git 平台调用，X-Woodpecker-Token 校验替代用户认证）
		v1.POST("/kbs/:kb/documents/:doc/webhook", d.Document.Webhook)
	}

	// 会话与问答（FR-07/09，API设计.md §3.7）：
	// 访问规则在 service 层（先校验库成员资格再校验所属用户，非成员/他人会话统一 404）；
	// 提问挂问答限流 10 req/min（API 设计 §1）
	if d.Chat != nil {
		convs := mustChangeOK.Group("/conversations", chatAccess(d.Chat, d.KBLookup))
		convs.GET("/:c", d.Chat.GetConversation)
		convs.PATCH("/:c", d.Chat.RenameConversation)
		convs.DELETE("/:c", d.Chat.DeleteConversation)
		convs.POST("/:c/messages", middleware.NewRateLimiter(10, time.Minute).Handler(middleware.UserKey), d.Chat.Ask)
		mustChangeOK.POST("/messages/:m/regenerate", d.Chat.Regenerate)
		mustChangeOK.POST("/messages/:m/feedback", d.Chat.Feedback)
		mustChangeOK.DELETE("/messages/:m/feedback", d.Chat.DeleteFeedback)
	}

	// admin（管理员）
	// 顺序：Auth → 限流 → Admin → 强制改密（限流必须在 Auth 之后才能取到 user key，P2-30）。
	// 越权优先返回 FORBIDDEN（不向无权限者泄露账号改密状态），
	// 管理员一旦被标记改密仍会被 AUTH_07 拦在业务接口之外。
	admin := v1.Group("/admin",
		middleware.Auth(d.Issuer, userLookup(d.UserRepo)),
		limiter.Handler(middleware.UserKey),
		middleware.Admin(),
		middleware.RequirePasswordChanged(),
	)
	{
		au := d.Admin
		admin.GET("/users", au.List)
		admin.POST("/users", au.Create)
		admin.PATCH("/users/:id", au.Patch)
		admin.POST("/users/:id/reset-password", au.ResetPassword)
		// M10：邮箱邀请制（FR-16）
		if d.Invite != nil {
			admin.POST("/invitations", d.Invite.CreateInvitations)
			admin.GET("/invitations", d.Invite.ListInvitations)
			admin.POST("/invitations/:id/resend", d.Invite.ResendInvitation)
			admin.DELETE("/invitations/:id", d.Invite.RevokeInvitation)
			admin.GET("/email-settings", d.Invite.GetEmailSettings)
			admin.PATCH("/email-settings", d.Invite.UpdateEmailSettings)
			admin.POST("/email-settings/test", d.Invite.TestEmail)
		}

		// M12：用户组（FR-18，管理员）
		if d.Groups != nil {
			gp := d.Groups
			admin.GET("/groups", gp.List)
			admin.POST("/groups", gp.Create)
			admin.PATCH("/groups/:id", gp.Update)
			admin.DELETE("/groups/:id", gp.Delete)
			admin.GET("/groups/:id/members", gp.ListMembers)
			admin.POST("/groups/:id/members", gp.AddMember)
			admin.DELETE("/groups/:id/members/:user", gp.RemoveMember)
		}

		// M13：OIDC 配置（FR-19，管理员）
		if d.OIDC != nil {
			admin.GET("/oidc-settings", d.OIDC.GetOidcSettings)
			admin.PATCH("/oidc-settings", d.OIDC.PatchOidcSettings)
			admin.POST("/oidc-settings/test", d.OIDC.TestOidcSettings)
		}

		// 模型服务配置（FR-12）
		mc := d.ModelCfg
		admin.GET("/model-services", mc.ListServices)
		admin.POST("/model-services", mc.CreateService)
		admin.PATCH("/model-services/:id", mc.UpdateService)
		admin.DELETE("/model-services/:id", mc.DeleteService)
		admin.POST("/model-services/:id/test", mc.TestConnection)
		admin.GET("/model-entries", mc.ListEntries)
		admin.POST("/model-entries", mc.CreateEntry)
		admin.PATCH("/model-entries/:id", mc.UpdateEntry)
		admin.DELETE("/model-entries/:id", mc.DeleteEntry)

		// 系统设置与审计/反馈查询（FR-13 / FR-14 / FR-15，API设计.md §3.4）
		if d.AdminSettings != nil {
			as := d.AdminSettings
			admin.GET("/settings", as.Get)
			admin.PATCH("/settings", as.Update)
			admin.GET("/audit-logs", as.ListAuditLogs)
			admin.GET("/feedbacks", as.ListFeedbacks)
		}
	}

	r.GET("/healthz", Health())                     // 基础设施探针挂在根路径（liveness：仅进程）
	r.GET("/readyz", Readyz(pinger, d.RedisPinger)) // readiness：依赖就绪判定

	if d.NoRoute != nil {
		r.NoRoute(d.NoRoute)
	}

	return r
}

// userLookup Auth 中间件的用户状态查询：一次回查同时提供 token_version 比对、
// 禁用校验与首登强制改密标记（审计 P1-4，避免每请求多一次查库）。
func userLookup(users UserLookupRepo) middleware.UserLookup {
	return func(c *gin.Context, userID int64) (middleware.UserState, error) {
		u, err := users.GetByID(c.Request.Context(), userID)
		if err != nil {
			return middleware.UserState{}, err
		}
		return middleware.UserState{
			Status:             u.Status,
			TokenVersion:       u.TokenVersion,
			MustChangePassword: u.MustChangePassword,
		}, nil
	}
}

// chatAccess 会话端点不做路径级拦截：成员资格与所属用户校验在 service.canAccess 统一处理
// （非成员与他人的会话统一 404，不泄露存在性）；该函数仅为分组占位（保持路由组结构清晰）。
func chatAccess(_ *ChatHandler, _ middleware.KBLookup) gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}
