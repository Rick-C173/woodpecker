package handler

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
)

// Pinger 依赖注入点：production 传 GORM Ping / Redis Ping，测试传 fake。
type Pinger func(ctx context.Context) error

// probeTimeout 单次依赖探测超时（探针要快失败，不能拖住 kubelet）。
const probeTimeout = 3 * time.Second

// probeDeps 统一依赖探测：数据库与该进程依赖的 Redis（asynq 队列 + 默认配置缓存）。
// 并发执行，避免串行叠加延迟；nil 探针视为「未配置该依赖」，按 up 处理。
func probeDeps(ctx context.Context, db, redis Pinger) (dbUp, redisUp bool) {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		dbUp = db == nil || db(pctx) == nil
	}()
	go func() {
		defer wg.Done()
		redisUp = redis == nil || redis(pctx) == nil
	}()
	wg.Wait()
	return dbUp, redisUp
}

// dependencyProbe 生成探针 handler：所有依赖就绪 → 200，否则 503。
// 响应体固定带 db/redis 两个字段（up|down），便于运维一眼定位是哪个依赖挂了；
// 503 时仍保持统一错误信封（code/message/request_id）与依赖明细并存（审计 P2-27）。
func dependencyProbe(db, redis Pinger) gin.HandlerFunc {
	return func(c *gin.Context) {
		dbUp, redisUp := probeDeps(c.Request.Context(), db, redis)
		dbState, redisState := upDown(dbUp), upDown(redisUp)
		if dbUp && redisUp {
			httpx.OK(c, gin.H{"status": "ok", "db": dbState, "redis": redisState})
			return
		}
		code, message := httpx.CodeDBUnreachable, "数据库不可用"
		if dbUp && !redisUp {
			code, message = httpx.CodeDBUnreachable, "Redis 不可用"
		} else if !dbUp && !redisUp {
			message = "数据库与 Redis 均不可用"
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":     "error",
			"code":       code,
			"message":    message,
			"request_id": c.GetString("request_id"),
			"db":         dbState,
			"redis":      redisState,
		})
	}
}

func upDown(ok bool) string {
	if ok {
		return "up"
	}
	return "down"
}

// Health GET /healthz：进程 + 数据库 + Redis 连通性探针（M0 验收标准）。
//
//	@Summary	健康检查（DB + Redis）
//	@Description	所有依赖就绪返回 {"status":"ok","db":"up","redis":"up"}；任一不可用返回 503 并指明是哪个依赖 down。
//	@Tags		infra
//	@Produce	json
//	@Success	200	{object}	object{status=string,db=string,redis=string}
//	@Failure	503	{object}	object{status=string,code=string,message=string,request_id=string,db=string,redis=string}
//	@Router		/healthz [get]
func Health(db, redis Pinger) gin.HandlerFunc {
	return dependencyProbe(db, redis)
}

// Readyz GET /readyz：就绪探针，语义与 /healthz 完全一致（所有依赖就绪才 200）。
// 复用 dependencyProbe，避免两处探测逻辑各自演化；供编排系统的 readinessProbe 使用，
// /healthz 供 livenessProbe 使用（两者当前判定一致，P2-27）。
//
//	@Summary	就绪检查（DB + Redis）
//	@Description	与 /healthz 同一探测逻辑：所有依赖就绪才 200。
//	@Tags		infra
//	@Produce	json
//	@Success	200	{object}	object{status=string,db=string,redis=string}
//	@Failure	503	{object}	object{status=string,code=string,message=string,request_id=string,db=string,redis=string}
//	@Router		/readyz [get]
func Readyz(db, redis Pinger) gin.HandlerFunc {
	return dependencyProbe(db, redis)
}
