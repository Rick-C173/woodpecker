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
			code, message = httpx.CodeRedisUnreachable, "Redis 不可用" // 审查 #2：SYS_001 语义是「数据库不可用」，勿混用
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

// Health GET /healthz：进程存活探针（livenessProbe 用）。
// 有意**不探测** DB/Redis（审查 #3）：liveness 失败会触发容器重启——依赖抖动时重启进程只会
// 放大故障；依赖是否就绪交给 /readyz（readinessProbe）判定。响应保持 M0 验收契约 {"status":"ok"}。
//
//	@Summary	存活检查（仅进程）
//	@Description	进程可服务请求即返回 {"status":"ok"}；不探测外部依赖，供 livenessProbe 使用。
//	@Tags		infra
//	@Produce	json
//	@Success	200	{object}	object{status=string}
//	@Router		/healthz [get]
func Health() gin.HandlerFunc {
	return func(c *gin.Context) {
		httpx.OK(c, gin.H{"status": "ok"})
	}
}

// Readyz GET /readyz：就绪探针（readinessProbe 用）：DB + Redis 全就绪才 200，否则 503
// 并指明是哪个依赖 down（审计 P2-27）。
//
//	@Summary	就绪检查（DB + Redis）
//	@Description	所有依赖就绪返回 {"status":"ok","db":"up","redis":"up"}；任一不可用返回 503 并指明是哪个依赖 down。
//	@Tags		infra
//	@Produce	json
//	@Success	200	{object}	object{status=string,db=string,redis=string}
//	@Failure	503	{object}	object{status=string,code=string,message=string,request_id=string,db=string,redis=string}
//	@Router		/readyz [get]
func Readyz(db, redis Pinger) gin.HandlerFunc {
	return dependencyProbe(db, redis)
}
