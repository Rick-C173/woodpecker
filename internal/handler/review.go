package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/service"
	"woodpecker/internal/store"
)

type ReviewHandler struct {
	reviewer *service.Reviewer
	store    *store.Store
}

func NewReviewHandler(reviewer *service.Reviewer, s *store.Store) *ReviewHandler {
	return &ReviewHandler{
		reviewer: reviewer,
		store:    s,
	}
}

type reviewRequest struct {
	Diff      string `json:"diff" binding:"required"`
	Language  string `json:"language"`
	ProjectID int    `json:"project_id"`
	PRNumber  int    `json:"pr_number"`
	PRTitle   string `json:"pr_title"`
	PRURL     string `json:"pr_url"`
	CommitSHA string `json:"commit_sha"`
	Branch    string `json:"branch"`
}

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
		DiffText:  req.Diff,
		Language:  req.Language,
		ProjectID: req.ProjectID,
		PRNumber:  req.PRNumber,
		PRTitle:   req.PRTitle,
		PRURL:     req.PRURL,
		CommitSHA: req.CommitSHA,
		Branch:    req.Branch,
	}

	resp := h.reviewer.Review(c.Request.Context(), svcReq)

	if resp.Error != "" && resp.ReviewID == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   resp.Error,
			"elapsed": resp.Elapsed,
		})
		return
	}

	result := gin.H{
		"review_id": resp.ReviewID,
		"elapsed":   resp.Elapsed,
	}

	if resp.Result != nil {
		result["result"] = resp.Result
	}

	if resp.Error != "" {
		result["warning"] = resp.Error
	}

	c.JSON(http.StatusOK, result)
}

func (h *ReviewHandler) Health(c *gin.Context) {
	status := gin.H{
		"status":  "healthy",
		"service": "woodpecker",
	}

	if h.store != nil {
		status["database"] = "connected"
	} else {
		status["database"] = "not configured"
	}

	c.JSON(http.StatusOK, status)
}
