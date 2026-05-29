package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/store"
)

type APIHandler struct {
	store *store.Store
}

func NewAPIHandler(s *store.Store) *APIHandler {
	return &APIHandler{store: s}
}

func (h *APIHandler) ListProjects(c *gin.Context) {
	projects, err := h.store.Reviews.GetProjects(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"projects": projects})
}

func (h *APIHandler) ListReviews(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")
	prStr := c.Query("pr_number")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	project, err := h.store.Reviews.GetProjectByOwnerRepo(c.Request.Context(), owner, repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	filter := store.ListReviewsFilter{
		Limit:  limit,
		Offset: offset,
	}

	if project != nil {
		filter.ProjectID = &project.ID
	}

	if prStr != "" {
		prNum, _ := strconv.Atoi(prStr)
		filter.PRNumber = &prNum
	}

	reviews, err := h.store.Reviews.ListReviews(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"reviews": reviews,
		"pagination": gin.H{
			"limit":  limit,
			"offset": offset,
		},
	})
}

func (h *APIHandler) GetReview(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid review id"})
		return
	}

	review, err := h.store.Reviews.GetReviewByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if review == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
		return
	}

	comments, err := h.store.Reviews.GetCommentsByReviewID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"review":   review,
		"comments": comments,
	})
}

func (h *APIHandler) GetStats(c *gin.Context) {
	owner := c.Query("owner")
	repo := c.Query("repo")

	var projectID *int
	if owner != "" && repo != "" {
		project, err := h.store.Reviews.GetProjectByOwnerRepo(c.Request.Context(), owner, repo)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if project != nil {
			projectID = &project.ID
		}
	}

	stats, err := h.store.Reviews.GetReviewStats(c.Request.Context(), projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, stats)
}
