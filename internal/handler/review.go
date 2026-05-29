package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/service"
)

// ReviewHandler 代码审查 HTTP 处理器
type ReviewHandler struct {
	reviewer *service.Reviewer
}

// NewReviewHandler 创建审查处理器
func NewReviewHandler(reviewer *service.Reviewer) *ReviewHandler {
	return &ReviewHandler{reviewer: reviewer}
}

// reviewRequest HTTP 请求体
type reviewRequest struct {
	Diff     string `json:"diff" binding:"required"` // git diff 文本
	Language string `json:"language"`                // 编程语言，可选
}

// Review 处理 POST /api/v1/review
func (h *ReviewHandler) Review(c *gin.Context) {
	var req reviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "请求参数无效",
			"detail":  err.Error(),
			"message": "请提供有效的 diff 字段（git diff 文本）",
		})
		return
	}

	svcReq := service.ReviewRequest{
		DiffText: req.Diff,
		Language: req.Language,
	}

	resp := h.reviewer.Review(c.Request.Context(), svcReq)

	if resp.Error != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   resp.Error,
			"elapsed": resp.Elapsed,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"result":  resp.Result,
		"elapsed": resp.Elapsed,
	})
}

// Health 健康检查 GET /health
func (h *ReviewHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"service": "woodpecker",
	})
}
