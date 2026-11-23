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

	KBLookup middleware.KBLookup
	// OnAdminKBAccess 管理员以非成员身份访问库时回调（审计）
	OnAdminKBAccess func(ctx context.Context, userID, kbID int64, method, path, ip string)

	Pinger Pinger // 可选：测试注入探针；为空时由 DB 派生
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
	v1.GET("/healthz", Health(pinger))

	// auth（公开）
	v1.POST("/auth/login", d.Auth.Login)
	v1.POST("/auth/register", d.Auth.Register)

	// auth（登录态）：全局限流（API 设计 §1，阈值可配 RATE_LIMIT_PER_MIN，<=0 关闭）
	limiter := middleware.NewRateLimiter(cfg.RateLimitPerMin, time.Minute)
	authed := v1.Group("", middleware.Auth(d.Issuer, userLookup(d.UserRepo)), limiter.Handler(middleware.UserKey))
	authed.POST("/auth/password", d.Auth.ChangePassword)

	// 知识库（FR-02）：建/改/删与成员授权仅管理员；库内读取需成员资格（R6 不可见原则）
	var adminBypass func(*gin.Context, int64, int64)
	if d.OnAdminKBAccess != nil {
		adminBypass = func(c *gin.Context, userID, kbID int64) {
			d.OnAdminKBAccess(c.Request.Context(), userID, kbID,
				c.Request.Method, c.Request.URL.Path, c.ClientIP())
		}
	}
	kbs := authed.Group("/kbs")
	kbs.GET("", d.KB.List)
	kbs.POST("", middleware.Admin(), d.KB.Create)
	kb := kbs.Group("/:kb", middleware.KBAccess(d.KBLookup, adminBypass))
	kb.GET("", d.KB.Get)
	kb.PATCH("", middleware.Admin(), d.KB.Update)
	kb.DELETE("", middleware.Admin(), d.KB.Delete)
	members := kb.Group("/members", middleware.Admin())
	members.GET("", d.KB.ListMembers)
	members.POST("", d.KB.AddMember)
	members.PATCH("/:user", d.KB.UpdateMemberRole)
	members.DELETE("/:user", d.KB.RemoveMember)

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
	}

	// admin（管理员）
	admin := v1.Group("/admin", middleware.Auth(d.Issuer, userLookup(d.UserRepo)), middleware.Admin())
	{
		au := d.Admin
		admin.GET("/users", au.List)
		admin.POST("/users", au.Create)
		admin.PATCH("/users/:id", au.Patch)
		admin.POST("/users/:id/reset-password", au.ResetPassword)

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
	}

	r.GET("/healthz", Health(pinger)) // 基础设施探针挂在根路径

	return r
}

// userLookup Auth 中间件的用户状态查询（token_version 比对 + 禁用校验）。
func userLookup(users UserLookupRepo) func(c *gin.Context, userID int64) (int16, int, error) {
	return func(c *gin.Context, userID int64) (int16, int, error) {
		u, err := users.GetByID(c.Request.Context(), userID)
		if err != nil {
			return 0, 0, err
		}
		return u.Status, u.TokenVersion, nil
	}
}
