package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Pinger 依赖注入点：production 传 GORM Ping，测试传 fake。
type Pinger func(ctx context.Context) error

// Health GET /healthz：进程与数据库连通性探针（M0 验收标准）。
//
//	@Summary	健康检查
//	@Tags		infra
//	@Produce	json
//	@Success	200	{object}	object{status=string,db=string}
//	@Failure	503	{object}	object{code=string,message=string,request_id=string}
//	@Router		/healthz [get]
func Health(pinger Pinger) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()

		if err := pinger(ctx); err != nil {
			FailWith(c, http.StatusServiceUnavailable, CodeDBUnreachable, "数据库不可用")
			return
		}
		OK(c, gin.H{"status": "ok", "db": "up"})
	}
}
