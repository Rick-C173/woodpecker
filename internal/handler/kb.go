package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// KBHandler 知识库与成员管理端点（FR-02 / R6）。
type KBHandler struct {
	svc *service.KBService
}

func NewKBHandler(svc *service.KBService) *KBHandler { return &KBHandler{svc: svc} }

// List GET /kbs：管理员=全部，普通用户=被授权库；均含 my_role。
//
//	@Summary	知识库列表（管理员=全部，成员=被授权库）
//	@Description	管理员返回全部知识库；普通用户仅返回被授权的库。每条记录带 my_role（R6 不可见原则）。
//	@Tags		kbs
//	@Produce	json
//	@Success	200	{object}	object{items=[]service.KBItem}
//	@Failure	401	{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs [get]
//	@Security	BearerAuth
func (h *KBHandler) List(c *gin.Context) {
	items, err := h.svc.List(c.Request.Context(), middleware.UserID(c), middleware.IsAdmin(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type createKBReq struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// Create POST /kbs（管理员）：名称必填 ≤50 字符，重名 → KB_001。
//
//	@Summary	新建知识库（管理员）
//	@Description	名称必填且 ≤50 字符；同名 → KB_001。
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		body	body		object{name=string,description=string}	true	"知识库名称与描述"
//	@Success	201		{object}	service.KBItem
//	@Failure	403		{object}	object{code=string,message=string,request_id=string}
//	@Failure	422		{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs [post]
//	@Security	BearerAuth
func (h *KBHandler) Create(c *gin.Context) {
	var req createKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "知识库名称不能为空")
		return
	}
	item, err := h.svc.Create(c.Request.Context(), middleware.UserID(c), req.Name, req.Description, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

// Get GET /kbs/{kb}（成员可见；非成员 404，由 KBAccess 拦截）。
//
//	@Summary	知识库详情（成员）
//	@Description	含 my_role 与 document_count；非成员一律 404（不泄露库存在性）。
//	@Tags		kbs
//	@Produce	json
//	@Param		kb	path		int	true	"知识库 ID"
//	@Success	200	{object}	service.KBItem
//	@Failure	404	{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs/{kb} [get]
//	@Security	BearerAuth
func (h *KBHandler) Get(c *gin.Context) {
	item, err := h.svc.Get(c.Request.Context(), middleware.KBID(c), middleware.UserID(c), middleware.IsAdmin(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

type patchKBReq struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// Update PATCH /kbs/{kb}（管理员）：字段缺省表示不修改。
//
//	@Summary	修改知识库（管理员）
//	@Description	字段缺省（不传）表示不修改；name 为空字符串 → 422。
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		kb		path		int											true	"知识库 ID"
//	@Param		body	body		object{name=string,description=string}	true	"待修改字段（均可缺省）"
//	@Success	200		{object}	service.KBItem
//	@Failure	404		{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs/{kb} [patch]
//	@Security	BearerAuth
func (h *KBHandler) Update(c *gin.Context) {
	var req patchKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "请求体非法")
		return
	}
	item, err := h.svc.Update(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), req.Name, req.Description, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

// Delete DELETE /kbs/{kb}（管理员）：级联删除文档/分块/会话，不可恢复。
//
//	@Summary	删除知识库（管理员，级联）
//	@Description	级联删除该库的文档、分块、会话与成员授权，不可恢复。
//	@Tags		kbs
//	@Produce	json
//	@Param		kb	path		int	true	"知识库 ID"
//	@Success	200	{object}	object{message=string}
//	@Failure	403	{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs/{kb} [delete]
//	@Security	BearerAuth
func (h *KBHandler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已删除"})
}

// ListMembers GET /kbs/{kb}/members（管理员）。
//
//	@Summary	成员列表（管理员）
//	@Tags		kbs
//	@Produce	json
//	@Param		kb	path		int	true	"知识库 ID"
//	@Success	200	{object}	object{items=[]service.KBMemberItem}
//	@Failure	403	{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs/{kb}/members [get]
//	@Security	BearerAuth
func (h *KBHandler) ListMembers(c *gin.Context) {
	items, err := h.svc.ListMembers(c.Request.Context(), middleware.KBID(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

type addMemberReq struct {
	UserID int64  `json:"user_id" binding:"required"`
	Role   string `json:"role" binding:"required"`
}

// AddMember POST /kbs/{kb}/members（管理员）。
//
//	@Summary	添加成员（管理员）
//	@Description	role 取值 viewer|editor|owner；用户不存在 → 404，重复授权 → 409。
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		kb		path		int									true	"知识库 ID"
//	@Param		body	body		object{user_id=int,role=string}	true	"用户与角色"
//	@Success	201		{object}	service.KBMemberItem
//	@Failure	404		{object}	object{code=string,message=string,request_id=string}
//	@Failure	409		{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs/{kb}/members [post]
//	@Security	BearerAuth
func (h *KBHandler) AddMember(c *gin.Context) {
	var req addMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "user_id 与 role 不能为空")
		return
	}
	item, err := h.svc.AddMember(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), req.UserID, req.Role, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

type patchMemberReq struct {
	Role string `json:"role" binding:"required"`
}

// UpdateMemberRole PATCH /kbs/{kb}/members/{user}（管理员）。
//
//	@Summary	修改成员角色（管理员）
//	@Description	不允许把库内最后一个 owner 降级（避免库失去管理者）。
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		kb		path		int						true	"知识库 ID"
//	@Param		user	path		int						true	"用户 ID"
//	@Param		body	body		object{role=string}	true	"新角色"
//	@Success	200		{object}	object{message=string}
//	@Failure	404		{object}	object{code=string,message=string,request_id=string}
//	@Failure	422		{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs/{kb}/members/{user} [patch]
//	@Security	BearerAuth
func (h *KBHandler) UpdateMemberRole(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user"), 10, 64)
	if err != nil || userID <= 0 {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "成员不存在")
		return
	}
	var req patchMemberReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "role 不能为空")
		return
	}
	if err := h.svc.UpdateMemberRole(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), userID, req.Role, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已更新"})
}

// RemoveMember DELETE /kbs/{kb}/members/{user}（管理员）。
//
//	@Summary	移除成员（管理员）
//	@Description	不允许移除库内最后一个 owner（避免库失去管理者）。
//	@Tags		kbs
//	@Produce	json
//	@Param		kb		path		int	true	"知识库 ID"
//	@Param		user	path		int	true	"用户 ID"
//	@Success	200		{object}	object{message=string}
//	@Failure	404		{object}	object{code=string,message=string,request_id=string}
//	@Failure	422		{object}	object{code=string,message=string,request_id=string}
//	@Router		/kbs/{kb}/members/{user} [delete]
//	@Security	BearerAuth
func (h *KBHandler) RemoveMember(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user"), 10, 64)
	if err != nil || userID <= 0 {
		httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "成员不存在")
		return
	}
	if err := h.svc.RemoveMember(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), userID, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已移除"})
}
