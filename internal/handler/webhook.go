package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/github"
	"woodpecker/internal/pipeline"
	"woodpecker/internal/store"
)

type WebhookController struct {
	webhookHandler *github.WebhookHandler
	processor      *pipeline.PRProcessor
	store          *store.Store
}

func NewWebhookController(wh *github.WebhookHandler, pr *pipeline.PRProcessor, s *store.Store) *WebhookController {
	return &WebhookController{
		webhookHandler: wh,
		processor:      pr,
		store:          s,
	}
}

func (c *WebhookController) Handle(ctx *gin.Context) {
	eventType := ctx.GetHeader("X-GitHub-Event")

	if eventType != "pull_request" {
		ctx.JSON(http.StatusOK, gin.H{"message": "event ignored: " + eventType})
		return
	}

	event, err := c.webhookHandler.ParseEvent(ctx.Request)
	if err != nil {
		log.Printf("Webhook 解析失败: %v", err)
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !c.webhookHandler.ShouldReview(event) {
		ctx.JSON(http.StatusOK, gin.H{
			"message": "action skipped",
			"action":  event.Action,
		})
		return
	}

	prInfo := github.ExtractPRInfo(event)

	if c.store != nil {
		project := &store.Project{
			Name:  fmt.Sprintf("%s/%s", prInfo.Owner, prInfo.Repo),
			Owner: prInfo.Owner,
			Repo:  prInfo.Repo,
		}
		projectID, err := c.store.Reviews.SaveProject(context.Background(), project)
		if err != nil {
			log.Printf("保存项目失败: %v", err)
		} else {
			prInfo.ProjectID = projectID
		}
	}

	log.Printf("Webhook 触发审查: PR #%d (%s/%s) - %s",
		prInfo.Number, prInfo.Owner, prInfo.Repo, event.Action)

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
