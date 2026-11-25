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
//	@Description	文档列表（分页，新到旧）。返回 {items,total,page,page_size}。
//	@Description
//	@Description	权限：库成员（R6：非成员按 404 处理，不泄露库名；管理员越权访问会记审计）。
//	@Description
//	@Description	操作类字段 can_sync/can_reindex/can_retry 按请求者权限与文档状态动态返回，前端据此渲染按钮。
//	@Tags		documents
//	@Produce	json
//	@Success	200	{object}	object{items=[]service.DocumentItem,total=int,page=int,page_size=int}
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Param	type	query	integer	false	"按来源类型过滤：1=git 2=upload 3=url（缺省=全部）"
//	@Param	status	query	integer	false	"按摄取状态过滤：1=pending 2=processing 3=ready 4=failed（缺省=全部）"
//	@Param	page	query	integer	false	"页码，默认 1"
//	@Param	page_size	query	integer	false	"每页条数，默认 20，最大 100"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
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
//	@Description	接入 Git 仓库，立即返回 201 与文档记录（status=pending），摄取任务异步执行。
//	@Description
//	@Description	credential 决定 worker 认证方式：kind=token 走 HTTPS BasicAuth（用户名取 GIT_TOKEN_USERNAME，默认 oauth2）；kind=ssh 用 PEM 私钥（需配置 known_hosts 校验）。向后兼容裸字符串（按 token 处理）。
//	@Description
//	@Description	权限：编辑者及以上（owner/editor，含管理员）。
//	@Description
//	@Description	错误：422 VALIDATION_FAILED（repo_url 为空 / credential 格式非法、kind 必须为 token 或 ssh、secret 非空）/ DOC_003 凭证无效或地址不可达；401。
//	@Tags		documents
//	@Accept		json
//	@Produce	json
//	@Param		body	body		object{repo_url=string,git_ref=string,credential=handler.CredentialInput,ignore_rules=[]string}	true	"仓库地址与凭证"
//	@Success	201	{object}	service.DocumentItem
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Failure	422	{object}	httpx.ErrorBody	"参数校验失败（repo_url 为空 / credential 格式非法）或 DOC_003 凭证无效、地址不可达"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
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
//	@Description	multipart 上传，每个文件创建一个独立文档，摄取任务异步执行。
//	@Description
//	@Description	限制（R2）：单次最多 50 个文件、单文件 5MB。请求体超过 50×5MB+1MB 直接 413（不读取文件内容）；文件数超过 50 返回 422。
//	@Description
//	@Description	格式不支持的文件逐个记入响应 rejected 数组（code=DOC_002），不影响其他文件继续入库。
//	@Description
//	@Description	权限：编辑者及以上（owner/editor，含管理员）。
//	@Tags		documents
//	@Accept		multipart/form-data
//	@Produce	json
//	@Success	201	{object}	object{items=[]service.DocumentItem,rejected=[]object}
//	@Failure	413	{object}	object{code=string,message=string,request_id=string}
//	@Failure	422	{object}	object{code=string,message=string,request_id=string}
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
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
//	@Description	接入网页，立即返回 201（status=pending），抓取与切块异步执行。网页类型的引用无 file_path/line 字段，改为 source_url。
//	@Description
//	@Description	权限：编辑者及以上（owner/editor，含管理员）。
//	@Description
//	@Description	错误：422 VALIDATION_FAILED（url 为空/非法）/ DOC_003 地址不可达。
//	@Tags		documents
//	@Accept		json
//	@Produce	json
//	@Success	201	{object}	service.DocumentItem
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Failure	422	{object}	httpx.ErrorBody	"url 为空/非法或 DOC_003 地址不可达"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
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
//	@Description	文档详情：基础字段 + 文件清单（含已排除标记与原因）+ 任务历史（新到旧）。
//	@Description
//	@Description	权限：库成员。
//	@Tags		documents
//	@Produce	json
//	@Success	200	{object}	service.DocumentDetail
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Param	doc	path	integer	true	"文档 ID"
//	@Failure	404	{object}	httpx.ErrorBody	"文档不存在或已删除（NOT_FOUND）"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
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
//	@Description	删除文档，级联删除分块、文件清单与任务历史；进行中的任务按僵尸任务语义收尾。删除写审计。
//	@Description
//	@Description	权限：管理者及以上（owner，含管理员）。
//	@Tags		documents
//	@Produce	json
//	@Success	200	{object}	object{message=string}
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Param	doc	path	integer	true	"文档 ID"
//	@Failure	404	{object}	httpx.ErrorBody	"文档不存在（NOT_FOUND）"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
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
//	@Description	对失败态文档重新发起摄取，产生一个全新任务（202 排队）。
//	@Description
//	@Description	仅 failed 状态可重试；同文档已有运行中任务时 409 DOC_TASK_RUNNING（任务幂等）。
//	@Description
//	@Description	权限：编辑者及以上（owner/editor，含管理员）。
//	@Tags		documents
//	@Produce	json
//	@Success	202	{object}	object{message=string}
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Param	doc	path	integer	true	"文档 ID"
//	@Failure	409	{object}	httpx.ErrorBody	"已有运行中任务（DOC_TASK_RUNNING）"
//	@Failure	404	{object}	httpx.ErrorBody	"文档不存在"
//	@Failure	422	{object}	httpx.ErrorBody	"仅失败态可重试"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
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
//	@Description	对已就绪文档全量重建索引（ReplaceDocumentChunks 语义：先清后建）。202 排队。
//	@Description
//	@Description	仅 ready 状态可重建；同文档已有运行中任务时 409 DOC_TASK_RUNNING。
//	@Description
//	@Description	权限：管理者及以上（owner，含管理员）。
//	@Tags		documents
//	@Produce	json
//	@Success	202	{object}	object{message=string}
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Param	doc	path	integer	true	"文档 ID"
//	@Failure	409	{object}	httpx.ErrorBody	"已有运行中任务（DOC_TASK_RUNNING）"
//	@Failure	404	{object}	httpx.ErrorBody	"文档不存在"
//	@Failure	422	{object}	httpx.ErrorBody	"仅就绪态可重建"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
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
//	@Description	手动触发 git 类型文档的增量同步：比对 last_commit 与远端 HEAD，变化的文件按文件替换（DeleteChunksByFile + InsertChunks），未变化文件不动。202 排队。
//	@Description
//	@Description	仅 git 类型可同步；同文档已有运行中任务时 409 DOC_TASK_RUNNING；同步失败（远端不可达）旧内容保持可用（DOC_004）。
//	@Description
//	@Description	权限：编辑者及以上（owner/editor，含管理员）。
//	@Tags		documents
//	@Produce	json
//	@Success	202	{object}	object{message=string}
//	@Param	kb	path	integer	true	"知识库 ID"
//	@Param	doc	path	integer	true	"文档 ID"
//	@Failure	409	{object}	httpx.ErrorBody	"已有运行中任务（DOC_TASK_RUNNING）"
//	@Failure	404	{object}	httpx.ErrorBody	"文档不存在"
//	@Failure	422	{object}	httpx.ErrorBody	"仅 git 类型可同步"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004 版本号不符，禁用/改密/设撤管理员后旧 token 立即失效）/ 账号被禁用（AUTH_003）"
//	@Router		/kbs/{kb}/documents/{doc}/sync [post]
func (h *DocumentHandler) Sync(c *gin.Context) {
	if err := h.svc.Sync(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), parseDocID(c), c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "同步任务已排队"})
}
