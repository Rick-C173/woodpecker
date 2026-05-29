package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/knowledge"
	"woodpecker/internal/qa"
	"woodpecker/internal/store"
)

type KnowledgeHandler struct {
	indexer       *knowledge.Indexer
	qaService     qa.QAService
	knowledgeRepo *store.KnowledgeRepository
}

func NewKnowledgeHandler(
	indexer *knowledge.Indexer,
	qaService qa.QAService,
	knowledgeRepo *store.KnowledgeRepository,
) *KnowledgeHandler {
	return &KnowledgeHandler{
		indexer:       indexer,
		qaService:     qaService,
		knowledgeRepo: knowledgeRepo,
	}
}

// IndexRepository 触发仓库索引
func (h *KnowledgeHandler) IndexRepository(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	if owner == "" || repo == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "owner and repo are required"})
		return
	}

	// 创建或更新索引状态
	idx, err := h.knowledgeRepo.CreateOrUpdateIndex(c.Request.Context(), owner, repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 异步触发索引（实际项目中应使用任务队列）
	go func() {
		err := h.indexer.IndexRepository(c.Request.Context(), owner, repo)
		if err != nil {
			_ = h.knowledgeRepo.UpdateIndexStatus(
				c.Request.Context(),
				owner, repo,
				"failed",
				0,
				err.Error(),
			)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"message": "indexing started",
		"index":   idx,
	})
}

// ListIndexedRepositories 列出已索引仓库
func (h *KnowledgeHandler) ListIndexedRepositories(c *gin.Context) {
	indexes, err := h.knowledgeRepo.ListAllIndexes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"indexes": indexes})
}

// GetIndexStatus 获取索引状态
func (h *KnowledgeHandler) GetIndexStatus(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	idx, err := h.knowledgeRepo.GetIndex(c.Request.Context(), owner, repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if idx == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "index not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"index": idx})
}

// DeleteIndex 删除索引
func (h *KnowledgeHandler) DeleteIndex(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	err := h.knowledgeRepo.DeleteIndex(c.Request.Context(), owner, repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// TODO: 同时删除向量数据库中的数据

	c.JSON(http.StatusOK, gin.H{"message": "index deleted"})
}

// Query 自然语言查询
func (h *KnowledgeHandler) Query(c *gin.Context) {
	var req qa.QueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is required"})
		return
	}

	if req.RepoOwner == "" || req.RepoName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_owner and repo_name are required"})
		return
	}

	resp, err := h.qaService.Query(c.Request.Context(), &req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ListQueryHistory 列出查询历史
func (h *KnowledgeHandler) ListQueryHistory(c *gin.Context) {
	owner := c.Param("owner")
	repo := c.Param("repo")

	limit := 50
	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := parseInt(limitStr); err == nil {
			limit = l
		}
	}

	histories, err := h.knowledgeRepo.ListQAHistory(c.Request.Context(), owner, repo, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"history": histories})
}

func parseInt(s string) (int, error) {
	var result int
	_, err := fmt.Sscanf(s, "%d", &result)
	return result, err
}
