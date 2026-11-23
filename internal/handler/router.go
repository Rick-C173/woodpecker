package handler

import (
	"context"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"woodpecker/internal/config"
	"woodpecker/internal/jwt"
	"woodpecker/internal/middleware"
	"woodpecker/internal/repo"
)

// Deps 组合根注入：main.go 构造 repos/services 后传入（技术选型 §5 分层）。
type Deps struct {
	Cfg      *config.Config
	DB       *gorm.DB
	Auth     *AuthHandler
	Admin    *AdminUsersHandler
	UserRepo *repo.UserRepo
	Issuer   *jwt.Issuer
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

	sqlDB, err := d.DB.DB()
	if err != nil {
		panic(err)
	}
	pinger := func(ctx context.Context) error { return sqlDB.PingContext(ctx) }

	v1 := r.Group("/api/v1")
	v1.GET("/healthz", Health(pinger))

	// auth（公开）
	v1.POST("/auth/login", d.Auth.Login)
	v1.POST("/auth/register", d.Auth.Register)

	// auth（登录态）
	authed := v1.Group("", middleware.Auth(d.Issuer, userLookup(d.UserRepo)))
	authed.POST("/auth/password", d.Auth.ChangePassword)

	// admin（管理员）
	admin := v1.Group("/admin", middleware.Auth(d.Issuer, userLookup(d.UserRepo)), middleware.Admin())
	{
		au := d.Admin
		admin.GET("/users", au.List)
		admin.POST("/users", au.Create)
		admin.PATCH("/users/:id", au.Patch)
		admin.POST("/users/:id/reset-password", au.ResetPassword)
	}

	r.GET("/healthz", Health(pinger)) // 基础设施探针挂在根路径

	return r
}

// userLookup Auth 中间件的用户状态查询（token_version 比对 + 禁用校验）。
func userLookup(users *repo.UserRepo) func(c *gin.Context, userID int64) (int16, int, error) {
	return func(c *gin.Context, userID int64) (int16, int, error) {
		u, err := users.GetByID(c.Request.Context(), userID)
		if err != nil {
			return 0, 0, err
		}
		return u.Status, u.TokenVersion, nil
	}
}
