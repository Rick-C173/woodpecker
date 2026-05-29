package handler

import (
	"context"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/github"
	"woodpecker/internal/pipeline"
)

// WebhookController GitHub Webhook 处理器
type WebhookController struct {
	webhookHandler *github.WebhookHandler
	processor      *pipeline.PRProcessor
}

// NewWebhookController 创建 Webhook 控制器
func NewWebhookController(wh *github.WebhookHandler, pr *pipeline.PRProcessor) *WebhookController {
	return &WebhookController{
		webhookHandler: wh,
		processor:      pr,
	}
}

// Handle 处理 POST /webhook
func (c *WebhookController) Handle(ctx *gin.Context) {
	eventType := ctx.GetHeader("X-GitHub-Event")

	// 只处理 pull_request 事件
	if eventType != "pull_request" {
		ctx.JSON(http.StatusOK, gin.H{"message": "event ignored: " + eventType})
		return
	}

	// 解析事件
	event, err := c.webhookHandler.ParseEvent(ctx.Request)
	if err != nil {
		log.Printf("Webhook 解析失败: %v", err)
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 判断是否需要审查
	if !c.webhookHandler.ShouldReview(event) {
		ctx.JSON(http.StatusOK, gin.H{
			"message": "action skipped",
			"action":  event.Action,
		})
		return
	}

	// 提取 PR 信息
	prInfo := github.ExtractPRInfo(event)

	log.Printf("Webhook 触发审查: PR #%d (%s/%s) - %s",
		prInfo.Number, prInfo.Owner, prInfo.Repo, event.Action)

	// 异步处理审查（避免 Webhook 超时）
	if c.processor != nil {
		go func() {
			if err := c.processor.Process(context.Background(), prInfo); err != nil {
				log.Printf("审查失败: PR #%d: %v", prInfo.Number, err)
			}
		}()
		ctx.JSON(http.StatusOK, gin.H{
			"message":   "review started",
			"pr_number": prInfo.Number,
		})
	} else {
		ctx.JSON(http.StatusOK, gin.H{
			"message": "GitHub 集成未启用，跳过审查",
		})
	}
}
