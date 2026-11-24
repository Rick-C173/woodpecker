package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

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

// CredentialInput Git 仓库凭证对象（API设计.md §5 / 数据库设计.md §3.2：{"kind","secret"}）。
// kind 决定 worker 走 HTTPS BasicAuth 还是 SSH PublicKeys（审计 P1-12）。
type CredentialInput struct {
	Kind   string `json:"kind" binding:"required,oneof=token ssh"`
	Secret string `json:"secret" binding:"required"`
}

// parseCredentialInput 解析请求体里的 credential 字段。
// 新契约为对象 {"kind","secret"}；**向后兼容**裸字符串（按 kind=token 处理，
// 避免旧前端/脚本直接 422——审计 P1-12）。空值表示匿名访问。
func parseCredentialInput(raw json.RawMessage) (service.GitCredential, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return service.GitCredential{}, nil
	}
	// 旧契约：裸字符串 → kind=token
	if strings.HasPrefix(trimmed, `"`) {
		var s string
		if err := json.Unmarshal([]byte(trimmed), &s); err != nil {
			return service.GitCredential{}, errCredentialFormat
		}
		return service.GitCredential{Kind: service.CredentialKindToken, Secret: s}, nil
	}
	var in CredentialInput
	if err := json.Unmarshal([]byte(trimmed), &in); err != nil {
		return service.GitCredential{}, errCredentialFormat
	}
	if in.Kind != service.CredentialKindToken && in.Kind != service.CredentialKindSSH {
		return service.GitCredential{}, errCredentialKind
	}
	if strings.TrimSpace(in.Secret) == "" {
		return service.GitCredential{}, errCredentialSecret
	}
	return service.GitCredential{Kind: in.Kind, Secret: in.Secret}, nil
}

var (
	errCredentialFormat = errors.New("credential 必须是 {kind, secret} 对象或字符串")
	errCredentialKind   = errors.New("credential.kind 必须是 token 或 ssh")
	errCredentialSecret = errors.New("credential.secret 不能为空")
)

// CreateGit POST /kbs/{kb}/documents/git（编辑者+）
//
//	@Summary	接入 Git 仓库
//	@Tags		documents
//	@Accept		json
//	@Produce	json
//	@Param		body	body		object{repo_url=string,git_ref=string,credential=handler.CredentialInput,ignore_rules=[]string}	true	"仓库地址与凭证"
//	@Success	201	{object}	service.DocumentItem
//	@Router		/kbs/{kb}/documents/git [post]
func (h *DocumentHandler) CreateGit(c *gin.Context) {
	var req struct {
		RepoURL     string          `json:"repo_url" binding:"required"`
		GitRef      string          `json:"git_ref"`
		Credential  json.RawMessage `json:"credential"`
		IgnoreRules []string        `json:"ignore_rules"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "repo_url 不能为空")
		return
	}
	cred, err := parseCredentialInput(req.Credential)
	if err != nil {
		httpx.Fail(c, httpx.CodeValidation, err.Error())
		return
	}
	item, serr := h.svc.CreateGit(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), req.RepoURL, req.GitRef, cred, req.IgnoreRules, c.ClientIP())
	if serr != nil {
		httpx.FailServiceError(c, serr)
		return
	}
	httpx.Created(c, item)
}

// 上传请求前置限制（审计 P2-28）：在读取任何文件之前就拒掉超限请求，
// 避免「先把几十个 5MB 文件全读进内存再逐个拒绝」。
const (
	maxUploadFiles = service.MaxUploadFiles // R2：单次最多 50 个
	// maxUploadBodyBytes 请求体上限：50×5MB + 1MB 余量（multipart 边界与其它字段）。
	maxUploadBodyBytes = maxUploadFiles*service.MaxUploadFileBytes + (1 << 20)
)

// uploadBodyLimit 请求体上限的实际取值；测试中可下调以覆盖 413 分支
// （不做成 const 是因为构造 >250MB 的真实请求体代价过大）。
var uploadBodyLimit int64 = maxUploadBodyBytes

// CreateUpload POST /kbs/{kb}/documents/upload（编辑者+）：multipart files[]。
//
//	@Summary	上传文件（每文件一个文档）
//	@Description	单次最多 50 个文件、单文件 5MB（R2）；请求体超限直接 413，文件数超限 422，均不读取文件内容。
//	@Tags		documents
//	@Accept		multipart/form-data
//	@Produce	json
//	@Success	201	{object}	object{items=[]service.DocumentItem,rejected=[]object}
//	@Failure	413	{object}	object{code=string,message=string,request_id=string}
//	@Failure	422	{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs/{kb}/documents/upload [post]
//	@Security	BearerAuth
func (h *DocumentHandler) CreateUpload(c *gin.Context) {
	// ① 请求体硬上限：multipart 解析阶段即受 MaxBytesReader 约束，超出返回 413。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, uploadBodyLimit)
	form, err := c.MultipartForm()
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.FailWith(c, http.StatusRequestEntityTooLarge, httpx.CodeDocRepoTooLarge,
				fmt.Sprintf("上传体积超出上限（单文件 5MB、单次最多 %d 个文件）", maxUploadFiles))
			return
		}
		httpx.Fail(c, httpx.CodeValidation, "需要 multipart/form-data 与 files[] 字段")
		return
	}
	// ② 文件数上限：先看数量，不读任何文件内容。
	fhs := form.File["files[]"]
	if len(fhs) > maxUploadFiles {
		httpx.Fail(c, httpx.CodeDocRepoTooLarge,
			fmt.Sprintf("单次上传不超过 %d 个文件", maxUploadFiles))
		return
	}
	files := make([]service.UploadFile, 0, len(fhs))
	for _, fh := range fhs {
		f, oerr := fh.Open()
		if oerr != nil {
			continue
		}
		content, rerr := io.ReadAll(io.LimitReader(f, service.MaxUploadFileBytes+1)) // 略超上限以便精确报错
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
