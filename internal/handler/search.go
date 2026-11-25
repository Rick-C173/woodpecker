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
//	@Description	纯检索（不产生会话）。流程：查询向量化 → 混合检索（向量 HNSW + trigram 全文，RRF 融合 Top-50）→ rerank 精排（缺省配置时降级 RRF 排序）→ 分页返回。
//	@Description
//	@Description	score 为融合得分，越小越相关。返回分块片段含快照字段（file_path/line/heading_path/source_url），与问答 citations 的引用结构一致。
//	@Description
//	@Description	权限：库成员。
//	@Tags		search
//	@Produce	json
//	@Success	200	{object}	service.SearchResult
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Param	q	query	string	true	"检索关键词（必填）"
//	@Param	page	query	integer	false	"页码，默认 1"
//	@Param	page_size	query	integer	false	"每页条数，默认 20"
//	@Failure	422	{object}	httpx.ErrorBody	"缺少 q 参数（VALIDATION_FAILED）"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
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
