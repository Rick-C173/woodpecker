package handler

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// DocumentHandler 文档接入与摄取（API设计.md §3.6）。
type DocumentHandler struct{ svc *service.DocumentService }

func NewDocumentHandler(svc *service.DocumentService) *DocumentHandler {
	return &DocumentHandler{svc: svc}
}

// parseDocID 路径参数 doc 解析（非法返回 0）。
func parseDocID(c *gin.Context) int64 {
	id, _ := strconv.ParseInt(c.Param("doc"), 10, 64)
	return id
}

// List GET /kbs/{kb}/documents?type=&status=&page=&page_size=
//
//	@Summary	文档列表
//	@Tags		documents
//	@Produce	json
//	@Success	200	{object}	object{items=[]service.DocumentItem,total=int,page=int,page_size=int}
//	@Router		/kbs/{kb}/documents [get]
func (h *DocumentHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	typ, _ := strconv.ParseInt(c.Query("type"), 10, 16)
	status, _ := strconv.ParseInt(c.Query("status"), 10, 16)
	items, total, err := h.svc.List(c.Request.Context(), middleware.KBID(c), int16(typ), int16(status), page, perPage)
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.PageOK(c, items, total, page, perPage)
}

// CreateGit POST /kbs/{kb}/documents/git（编辑者+）
//
//	@Summary	接入 Git 仓库
//	@Tags		documents
//	@Accept		json
//	@Produce	json
//	@Success	201	{object}	service.DocumentItem
//	@Router		/kbs/{kb}/documents/git [post]
func (h *DocumentHandler) CreateGit(c *gin.Context) {
	var req struct {
		RepoURL     string   `json:"repo_url" binding:"required"`
		GitRef      string   `json:"git_ref"`
		Credential  string   `json:"credential"`
		IgnoreRules []string `json:"ignore_rules"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "repo_url 不能为空")
		return
	}
	item, err := h.svc.CreateGit(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), req.RepoURL, req.GitRef, req.Credential, req.IgnoreRules, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

// CreateUpload POST /kbs/{kb}/documents/upload（编辑者+）：multipart files[]。
//
//	@Summary	上传文件（每文件一个文档）
//	@Tags		documents
//	@Accept		multipart/form-data
//	@Produce	json
//	@Success	201	{object}	object{items=[]service.DocumentItem,rejected=[]object}
//	@Router		/kbs/{kb}/documents/upload [post]
func (h *DocumentHandler) CreateUpload(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		httpx.Fail(c, httpx.CodeValidation, "需要 multipart/form-data 与 files[] 字段")
		return
	}
	fhs := form.File["files[]"]
	files := make([]service.UploadFile, 0, len(fhs))
	for _, fh := range fhs {
		f, oerr := fh.Open()
		if oerr != nil {
			continue
		}
		content, rerr := io.ReadAll(io.LimitReader(f, 6<<20)) // 略超上限以便精确报错
		_ = f.Close()
		if rerr != nil {
			continue
		}
		files = append(files, service.UploadFile{Filename: fh.Filename, Content: content})
	}
	items, rejected, err := h.svc.CreateUpload(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), files, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	body := gin.H{"items": items}
	if len(rejected) > 0 {
		rej := make([]gin.H, 0, len(rejected))
		for _, r := range rejected {
			rej = append(rej, gin.H{"code": r.Code, "message": r.Message})
		}
		body["rejected"] = rej
	}
	httpx.Created(c, body)
}

// CreateURL POST /kbs/{kb}/documents/url（编辑者+）
//
//	@Summary	接入网页
//	@Tags		documents
//	@Accept		json
//	@Produce	json
//	@Success	201	{object}	service.DocumentItem
//	@Router		/kbs/{kb}/documents/url [post]
func (h *DocumentHandler) CreateURL(c *gin.Context) {
	var req struct {
		URL string `json:"url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "url 不能为空")
		return
	}
	item, err := h.svc.CreateURL(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), req.URL, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

// Get GET /kbs/{kb}/documents/{doc}（成员）
//
//	@Summary	文档详情（状态、文件清单、任务历史）
//	@Tags		documents
//	@Produce	json
//	@Success	200	{object}	service.DocumentDetail
//	@Router		/kbs/{kb}/documents/{doc} [get]
func (h *DocumentHandler) Get(c *gin.Context) {
	item, err := h.svc.Get(c.Request.Context(), middleware.KBID(c), parseDocID(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

// Delete DELETE /kbs/{kb}/documents/{doc}（管理者+）
//
//	@Summary	删除文档（级联清分块/清单/任务）
//	@Tags		documents
//	@Produce	json
//	@Success	200	{object}	object{message=string}
//	@Router		/kbs/{kb}/documents/{doc} [delete]
func (h *DocumentHandler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), parseDocID(c), c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已删除"})
}

// Retry POST /kbs/{kb}/documents/{doc}/retry（编辑者+）：仅失败态，202。
//
//	@Summary	重试摄取（产生全新任务）
//	@Tags		documents
//	@Produce	json
//	@Success	202	{object}	object{message=string}
//	@Router		/kbs/{kb}/documents/{doc}/retry [post]
func (h *DocumentHandler) Retry(c *gin.Context) {
	if err := h.svc.Retry(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), parseDocID(c), c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "重试任务已排队"})
}

// Reindex POST /kbs/{kb}/documents/{doc}/reindex（管理者+）：仅就绪态，202。
//
//	@Summary	重建索引
//	@Tags		documents
//	@Produce	json
//	@Success	202	{object}	object{message=string}
//	@Router		/kbs/{kb}/documents/{doc}/reindex [post]
func (h *DocumentHandler) Reindex(c *gin.Context) {
	if err := h.svc.Reindex(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), parseDocID(c), c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "重建索引任务已排队"})
}

// Sync POST /kbs/{kb}/documents/{doc}/sync（编辑者+）：仅 git 类型，202。
//
//	@Summary	手动同步（增量比对 last_commit）
//	@Tags		documents
//	@Produce	json
//	@Success	202	{object}	object{message=string}
//	@Router		/kbs/{kb}/documents/{doc}/sync [post]
func (h *DocumentHandler) Sync(c *gin.Context) {
	if err := h.svc.Sync(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), parseDocID(c), c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "同步任务已排队"})
}
