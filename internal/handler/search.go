package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// SearchHandler 检索页（FR-10，API设计.md §3.7）。
type SearchHandler struct{ svc *service.SearchService }

func NewSearchHandler(svc *service.SearchService) *SearchHandler {
	return &SearchHandler{svc: svc}
}

// Search GET /kbs/{kb}/search?q=&page=&page_size=（成员）
//
//	@Summary	混合检索（向量 HNSW + trigram，RRF 融合，rerank 精排）
//	@Tags		search
//	@Produce	json
//	@Success	200	{object}	service.SearchResult
//	@Router		/kbs/{kb}/search [get]
func (h *SearchHandler) Search(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	result, err := h.svc.Search(c.Request.Context(), middleware.KBID(c), c.Query("q"), page, perPage)
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, result)
}
