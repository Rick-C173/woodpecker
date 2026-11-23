package handler

import (
	"context"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"woodpecker/internal/config"
	"woodpecker/internal/middleware"
)

// NewRouter 装配全局中间件与 /api/v1 路由骨架。
// 后续里程碑在此按模块挂载：auth(M1) → kbs/documents(M2/M4) → search(M5) → conversations(M6) → admin(M1/M3/M7)。
func NewRouter(cfg *config.Config, db *gorm.DB) *gin.Engine {
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

	sqlDB, err := db.DB()
	if err != nil {
		panic(err)
	}
	pinger := func(ctx context.Context) error { return sqlDB.PingContext(ctx) }

	v1 := r.Group("/api/v1")
	v1.GET("/healthz", Health(pinger))
	r.GET("/healthz", Health(pinger)) // 基础设施探针挂在根路径

	return r
}
