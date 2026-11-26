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
//	@Description	我可见的知识库列表（管理员=全部，普通用户=所在库），每条含 my_role（owner/editor/member）。返回 {items:[...]}，不分页。
//	@Description
//	@Description	权限：登录。
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
	Visibility  int16  `json:"visibility"` // 1=私有（缺省）2=全员可见（FR-17）
}

// Create POST /kbs（管理员）：名称必填 ≤50 字符，重名 → KB_001。
//
//	@Summary	新建知识库（管理员）
//	@Description	建库。名称不超过 50 字符且同账号下唯一。
//	@Description
//	@Description	错误：409 KB_001 知识库名称重复；422 VALIDATION_FAILED。
//	@Description
//	@Description	权限：管理员。
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		body	body		object{name=string,description=string,visibility=int}	true	"名称/描述/可见性（1=私有 2=全员可见，缺省私有）"
//	@Success	201		{object}	service.KBItem
//	@Failure	403		{object}	object{code=string,message=string,request_id=string}
//	@Failure	422		{object}	object{code=string,message=string,request_id=string}
//	@Failure	409	{object}	httpx.ErrorBody	"名称重复 KB_001"
//	@Router		/kbs [post]
//	@Security	BearerAuth
func (h *KBHandler) Create(c *gin.Context) {
	var req createKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "知识库名称不能为空")
		return
	}
	item, err := h.svc.Create(c.Request.Context(), middleware.UserID(c), req.Name, req.Description, req.Visibility, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

// Get GET /kbs/{kb}（成员可见；非成员 404，由 KBAccess 拦截）。
//
//	@Summary	知识库详情（成员）
//	@Description	库详情。**R6 不可见原则：非成员按 404 返回（NOT_FOUND），不泄露库名存在性**；管理员以非成员身份访问会记审计（kb.access.denied_admin_bypass）。
//	@Description
//	@Description	权限：库成员（或管理员）。
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
	Visibility  *int16  `json:"visibility"` // 1=私有 2=全员可见（FR-17）
}

// Update PATCH /kbs/{kb}（管理员）：字段缺省表示不修改。
//
//	@Summary	修改知识库（管理员）
//	@Description	改名/改描述，字段缺省不修改。
//	@Description
//	@Description	错误：409 KB_001 名称重复。
//	@Description
//	@Description	权限：管理员。
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		kb		path		int											true	"知识库 ID"
//	@Param		body	body		object{name=string,description=string,visibility=int}	true	"待修改字段（均可缺省）"
//	@Success	200		{object}	service.KBItem
//	@Failure	404		{object}	object{code=string,message=string,request_id=string}
//	@Failure	409	{object}	httpx.ErrorBody	"名称重复 KB_001"
//	@Failure	422	{object}	httpx.ErrorBody	"VALIDATION_FAILED"
//	@Router		/kbs/{kb} [patch]
//	@Security	BearerAuth
func (h *KBHandler) Update(c *gin.Context) {
	var req patchKBReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "请求体非法")
		return
	}
	item, err := h.svc.Update(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), req.Name, req.Description, req.Visibility, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

// Delete DELETE /kbs/{kb}（管理员）：级联删除文档/分块/会话，不可恢复。
//
//	@Summary	删除知识库（管理员，级联）
//	@Description	删库，**级联删除**库内文档、分块、文件清单、任务历史、会话与反馈。二次确认由前端承担。
//	@Description
//	@Description	权限：管理员。
//	@Tags		kbs
//	@Produce	json
//	@Param		kb	path		int	true	"知识库 ID"
//	@Success	200	{object}	object{message=string}
//	@Failure	403	{object}	object{code=string,message=string,request_id=string}
//	@Failure	404	{object}	httpx.ErrorBody	"不存在 NOT_FOUND"
//	@Router		/kbs/{kb} [delete]
//	@Security	BearerAuth
func (h *KBHandler) Delete(c *gin.Context) {
	if err := h.svc.Delete(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已删除"})
}

// ListMembers GET /kbs/{kb}/members（管理员或管理者）。
//
//	@Summary	成员列表（管理员/管理者）
//	@Description	成员列表（user_id/username/role/加入时间）。
//	@Description
//	@Description	角色定义（PRD R6）：owner=管理者（可删改内容、授权成员）editor=编辑者（可补充内容）member=成员（只读）。
//	@Description
//	@Description	权限：管理员或库内管理者。
//	@Tags		kbs
//	@Produce	json
//	@Param		kb	path		int	true	"知识库 ID"
//	@Success	200	{object}	object{items=[]service.KBMemberItem}
//	@Failure	403	{object}	object{code=string,message=string,request_id=string}
//	@Failure	404	{object}	httpx.ErrorBody	"非成员/不存在 NOT_FOUND"
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

// AddMember POST /kbs/{kb}/members（管理员或管理者）。
//
//	@Summary	添加成员（管理员/管理者）
//	@Description	添加成员并指派角色：{user_id, role: owner|editor|member}。
//	@Description
//	@Description	错误：404 目标用户不存在；409 已是成员。
//	@Description
//	@Description	权限：管理员或库内管理者（R6）。
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		kb		path		int									true	"知识库 ID"
//	@Param		body	body		object{user_id=int,role=string}	true	"用户与角色"
//	@Success	201		{object}	service.KBMemberItem
//	@Failure	404		{object}	object{code=string,message=string,request_id=string}
//	@Failure	409		{object}	object{code=string,message=string,request_id=string}
//	@Failure	422	{object}	httpx.ErrorBody	"role 非法 VALIDATION_FAILED"
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

// UpdateMemberRole PATCH /kbs/{kb}/members/{user}（管理员或管理者）。
//
//	@Summary	修改成员角色（管理员/管理者）
//	@Description	变更成员角色（owner|editor|member）；管理者不能变更自己的角色（403，需管理员处理）。
//	@Description
//	@Description	权限：管理员或库内管理者（R6）。
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		kb		path		int						true	"知识库 ID"
//	@Param		user	path		int						true	"用户 ID"
//	@Param		body	body		object{role=string}	true	"新角色"
//	@Success	200		{object}	object{message=string}
//	@Failure	403		{object}	object{code=string,message=string,request_id=string}
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
	// owner 不可变更自己的角色（防锁死，需管理员处理；FR-02）
	if !middleware.IsAdmin(c) && userID == middleware.UserID(c) {
		httpx.Fail(c, httpx.CodeForbidden, "不能变更自己的角色，请联系管理员")
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

// RemoveMember DELETE /kbs/{kb}/members/{user}（管理员或管理者）。
//
//	@Summary	移除成员（管理员/管理者）
//	@Description	移除成员，**其历史会话保留但立即失访**（后续访问会话 404）；管理者不能移除自己（403，需管理员处理）。
//	@Description
//	@Description	权限：管理员或库内管理者（R6）。
//	@Tags		kbs
//	@Produce	json
//	@Param		kb		path		int	true	"知识库 ID"
//	@Param		user	path		int	true	"用户 ID"
//	@Success	200		{object}	object{message=string}
//	@Failure	403		{object}	object{code=string,message=string,request_id=string}
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
	// owner 不可移除自己（防锁死，需管理员处理；FR-02）
	if !middleware.IsAdmin(c) && userID == middleware.UserID(c) {
		httpx.Fail(c, httpx.CodeForbidden, "不能移除自己，请联系管理员")
		return
	}
	if err := h.svc.RemoveMember(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), userID, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已移除"})
}
