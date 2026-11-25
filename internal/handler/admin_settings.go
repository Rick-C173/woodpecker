package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// AdminSettingsHandler 系统设置与审计/反馈查询（FR-13 / FR-14 / FR-15，API设计.md §3.4）。
type AdminSettingsHandler struct {
	settings *service.SettingsService
	query    *service.AdminQueryService
}

func NewAdminSettingsHandler(settings *service.SettingsService, query *service.AdminQueryService) *AdminSettingsHandler {
	return &AdminSettingsHandler{settings: settings, query: query}
}

// Get GET /admin/settings
//
//	@Summary	系统设置当前值
//	@Description	系统设置当前值（FR-13）：排除规则与自助注册开关。exclusion_rules 为 null 表示未自定义、使用内置默认清单（README/锁文件等）。
//	@Description
//	@Description	权限：管理员。缺省值直接展示，前端一次拉全。
//	@Tags		settings
//	@Produce	json
//	@Success	200	{object}	service.SettingsView
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）；非管理员 403 FORBIDDEN"
//	@Router		/admin/settings [get]
func (h *AdminSettingsHandler) Get(c *gin.Context) {
	v, err := h.settings.Get(c.Request.Context())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, v)
}

// Update PATCH /admin/settings
//
//	@Summary	更新系统设置（记审计）
//	@Description	更新系统设置（记审计 action=setting.update）。字段缺省不修改；至少提供一项，否则 422。
//	@Description
//	@Description	exclusion_rules 传 glob 数组覆盖自定义清单；传空数组=清空自定义（回到默认清单）。self_register 控制自助注册开关（默认关闭，FR-01）。
//	@Description
//	@Description	权限：管理员。
//	@Tags		settings
//	@Accept		json
//	@Produce	json
//	@Success	200	{object}	object{message=string}
//	@Failure	422	{object}	httpx.ErrorBody	"请求体非法或未提供任何要更新的设置（VALIDATION_FAILED）"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）；非管理员 403 FORBIDDEN"
//	@Router		/admin/settings [patch]
func (h *AdminSettingsHandler) Update(c *gin.Context) {
	var req service.UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "请求体非法")
		return
	}
	if req.ExclusionRules == nil && req.SelfRegister == nil {
		httpx.Fail(c, httpx.CodeValidation, "至少提供一项要更新的设置")
		return
	}
	if err := h.settings.Update(c.Request.Context(), middleware.UserID(c), req, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "设置已更新"})
}

// parseTimeQuery 解析 RFC3339 时间筛选（from/to）。
func parseTimeQuery(c *gin.Context, key string) (*time.Time, error) {
	raw := c.Query(key)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListAuditLogs GET /admin/audit-logs
//
//	@Summary	审计日志查询（筛选分页）
//	@Description	审计日志查询（分页，新到旧）。所有写操作与越权尝试均记录（FR-15）；detail 为动作相关附加信息。
//	@Description
//	@Description	权限：管理员。
//	@Tags		audit
//	@Produce	json
//	@Param		user_id		query	int		false	"操作人"
//	@Param		action		query	string	false	"动作"
//	@Param		object_type	query	string	false	"对象类型"
//	@Param		from		query	string	false	"起始时间 RFC3339"
//	@Param		to			query	string	false	"截止时间 RFC3339"
//	@Success	200	{object}	object{items=[]repo.AuditRow,total=int,page=int,page_size=int}
//	@Param	page	query	integer	false	"页码，默认 1"
//	@Param	page_size	query	integer	false	"每页条数，默认 20，最大 100"
//	@Failure	422	{object}	httpx.ErrorBody	"from/to 不是合法 RFC3339（VALIDATION_FAILED）"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）；非管理员 403 FORBIDDEN"
//	@Router		/admin/audit-logs [get]
func (h *AdminSettingsHandler) ListAuditLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	userID, _ := strconv.ParseInt(c.Query("user_id"), 10, 64)
	from, ferr := parseTimeQuery(c, "from")
	to, terr := parseTimeQuery(c, "to")
	if ferr != nil || terr != nil {
		httpx.Fail(c, httpx.CodeValidation, "from/to 须为 RFC3339 时间")
		return
	}
	items, total, err := h.query.ListAuditLogs(c.Request.Context(), service.AdminAuditQuery{
		UserID: userID, Action: c.Query("action"), ObjectType: c.Query("object_type"),
		From: from, To: to, Page: page, PerPage: perPage,
	})
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.PageOK(c, items, total, page, perPage)
}

// ListFeedbacks GET /admin/feedbacks
//
//	@Summary	反馈列表（FR-14，筛选分页）
//	@Description	点踩反馈列表（FR-14，分页，新到旧）。联查被点踩回答与反馈人，message_text 为回答内容截断摘要。
//	@Description
//	@Description	权限：管理员。
//	@Tags		audit
//	@Produce	json
//	@Param		kb_id	query	int		false	"知识库"
//	@Param		reason	query	int		false	"原因 1-4"
//	@Param		from	query	string	false	"起始时间 RFC3339"
//	@Param		to		query	string	false	"截止时间 RFC3339"
//	@Success	200	{object}	object{items=[]repo.FeedbackAdminRow,total=int,page=int,page_size=int}
//	@Param	page	query	integer	false	"页码，默认 1"
//	@Param	page_size	query	integer	false	"每页条数，默认 20，最大 100"
//	@Failure	422	{object}	httpx.ErrorBody	"from/to 不是合法 RFC3339（VALIDATION_FAILED）"
//	@Failure	401	{object}	httpx.ErrorBody	"未登录 / token 失效（AUTH_004）/ 账号被禁用（AUTH_003）；非管理员 403 FORBIDDEN"
//	@Router		/admin/feedbacks [get]
func (h *AdminSettingsHandler) ListFeedbacks(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	kbID, _ := strconv.ParseInt(c.Query("kb_id"), 10, 64)
	reason, _ := strconv.Atoi(c.Query("reason"))
	from, ferr := parseTimeQuery(c, "from")
	to, terr := parseTimeQuery(c, "to")
	if ferr != nil || terr != nil {
		httpx.Fail(c, httpx.CodeValidation, "from/to 须为 RFC3339 时间")
		return
	}
	items, total, err := h.query.ListFeedbacks(c.Request.Context(), service.AdminFeedbackQuery{
		KBID: kbID, Reason: int16(reason), From: from, To: to, Page: page, PerPage: perPage,
	})
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.PageOK(c, items, total, page, perPage)
}
