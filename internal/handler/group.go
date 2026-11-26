package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// GroupHandler 用户组与组授权（FR-18）：管理端组管理 + KB 组授权。
type GroupHandler struct {
	svc *service.GroupService
}

func NewGroupHandler(svc *service.GroupService) *GroupHandler {
	return &GroupHandler{svc: svc}
}

// List GET /admin/groups（管理员）。
//
//	@Summary	用户组列表（管理员）
//	@Description	含成员数与授权库数。
//	@Tags		groups
//	@Produce	json
//	@Success	200	{object}	object{items=[]service.GroupItem}
//	@Router		/admin/groups [get]
//	@Security	BearerAuth
func (h *GroupHandler) List(c *gin.Context) {
	items, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

// Create POST /admin/groups（管理员）。
//
//	@Summary	建组（管理员）
//	@Description	name 全局唯一 ≤50 字符；重名 409 GROUP_001。
//	@Tags		groups
//	@Accept		json
//	@Produce	json
//	@Param		body	body		object{name=string,description=string}	true	"组名与描述"
//	@Success	201		{object}	service.GroupItem
//	@Failure	409		{object}	httpx.ErrorBody	"GROUP_001"
//	@Router		/admin/groups [post]
//	@Security	BearerAuth
func (h *GroupHandler) Create(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "组名不能为空")
		return
	}
	item, err := h.svc.Create(c.Request.Context(), middleware.UserID(c), req.Name, req.Description, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

// Update PATCH /admin/groups/{id}（管理员）。
//
//	@Summary	改组（管理员）
//	@Tags		groups
//	@Accept		json
//	@Produce	json
//	@Param		id		path		int										true	"组 ID"
//	@Param		body	body		object{name=string,description=string}	true	"待修改字段（均可缺省）"
//	@Success	200		{object}	service.GroupItem
//	@Router		/admin/groups/{id} [patch]
//	@Security	BearerAuth
func (h *GroupHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, 404, httpx.CodeNotFound, "用户组不存在")
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "请求体非法")
		return
	}
	item, err := h.svc.Update(c.Request.Context(), middleware.UserID(c), id, req.Name, req.Description, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

// Delete DELETE /admin/groups/{id}（管理员，级联）。
//
//	@Summary	删组（管理员，级联解除组员与授权）
//	@Tags		groups
//	@Produce	json
//	@Param		id	path		int	true	"组 ID"
//	@Success	200	{object}	object{message=string}
//	@Router		/admin/groups/{id} [delete]
//	@Security	BearerAuth
func (h *GroupHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, 404, httpx.CodeNotFound, "用户组不存在")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), middleware.UserID(c), id, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已删除"})
}

// ListMembers GET /admin/groups/{id}/members（管理员）。
//
//	@Summary	组成员列表（管理员）
//	@Tags		groups
//	@Produce	json
//	@Param		id	path		int	true	"组 ID"
//	@Success	200	{object}	object{items=[]service.GroupMemberItem}
//	@Router		/admin/groups/{id}/members [get]
//	@Security	BearerAuth
func (h *GroupHandler) ListMembers(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, 404, httpx.CodeNotFound, "用户组不存在")
		return
	}
	items, err := h.svc.ListMembers(c.Request.Context(), id)
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

// AddMember POST /admin/groups/{id}/members（管理员）。
//
//	@Summary	加组员（管理员）
//	@Description	用户不存在 404；已在组内 409 GROUP_002。
//	@Tags		groups
//	@Accept		json
//	@Produce	json
//	@Param		id		path		int					true	"组 ID"
//	@Param		body	body		object{user_id=int}	true	"用户"
//	@Success	201		{object}	service.GroupMemberItem
//	@Failure	409		{object}	httpx.ErrorBody	"GROUP_002"
//	@Router		/admin/groups/{id}/members [post]
//	@Security	BearerAuth
func (h *GroupHandler) AddMember(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, 404, httpx.CodeNotFound, "用户组不存在")
		return
	}
	var req struct {
		UserID int64 `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "user_id 不能为空")
		return
	}
	item, err := h.svc.AddMember(c.Request.Context(), middleware.UserID(c), id, req.UserID, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

// RemoveMember DELETE /admin/groups/{id}/members/{user}（管理员）。
//
//	@Summary	移出组（管理员，即时失去组授库访问）
//	@Tags		groups
//	@Produce	json
//	@Param		id		path		int	true	"组 ID"
//	@Param		user	path		int	true	"用户 ID"
//	@Success	200		{object}	object{message=string}
//	@Router		/admin/groups/{id}/members/{user} [delete]
//	@Security	BearerAuth
func (h *GroupHandler) RemoveMember(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.FailWith(c, 404, httpx.CodeNotFound, "用户组不存在")
		return
	}
	userID, err := strconv.ParseInt(c.Param("user"), 10, 64)
	if err != nil || userID <= 0 {
		httpx.FailWith(c, 404, httpx.CodeNotFound, "成员不存在")
		return
	}
	if err := h.svc.RemoveMember(c.Request.Context(), middleware.UserID(c), id, userID, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已移除"})
}

// ListGrants GET /kbs/{kb}/group-roles（管理员/管理者）。
//
//	@Summary	组授权列表（管理员/管理者）
//	@Tags		kbs
//	@Produce	json
//	@Param		kb	path		int	true	"知识库 ID"
//	@Success	200	{object}	object{items=[]service.KBGroupRoleItem}
//	@Router		/kbs/{kb}/group-roles [get]
//	@Security	BearerAuth
func (h *GroupHandler) ListGrants(c *gin.Context) {
	items, err := h.svc.ListGrantsByKB(c.Request.Context(), middleware.KBID(c))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": items})
}

// Grant POST /kbs/{kb}/group-roles（管理员/管理者）。
//
//	@Summary	组授权（管理员/管理者）
//	@Description	{group_id, role: owner|editor|member}；已授权 409 GROUP_003。
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		kb		path		int									true	"知识库 ID"
//	@Param		body	body		object{group_id=int,role=string}	true	"组与角色"
//	@Success	201		{object}	service.KBGroupRoleItem
//	@Failure	409		{object}	httpx.ErrorBody	"GROUP_003"
//	@Router		/kbs/{kb}/group-roles [post]
//	@Security	BearerAuth
func (h *GroupHandler) Grant(c *gin.Context) {
	var req struct {
		GroupID int64  `json:"group_id" binding:"required"`
		Role    string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "group_id 与 role 不能为空")
		return
	}
	item, err := h.svc.Grant(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), req.GroupID, req.Role, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, item)
}

// UpdateGrant PATCH /kbs/{kb}/group-roles/{group}（管理员/管理者）。
//
//	@Summary	变更组角色（管理员/管理者）
//	@Tags		kbs
//	@Accept		json
//	@Produce	json
//	@Param		kb		path		int						true	"知识库 ID"
//	@Param		group	path		int						true	"组 ID"
//	@Param		body	body		object{role=string}	true	"新角色"
//	@Success	200		{object}	object{message=string}
//	@Router		/kbs/{kb}/group-roles/{group} [patch]
//	@Security	BearerAuth
func (h *GroupHandler) UpdateGrant(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("group"), 10, 64)
	if err != nil || groupID <= 0 {
		httpx.FailWith(c, 404, httpx.CodeNotFound, "组授权不存在")
		return
	}
	var req struct {
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "role 不能为空")
		return
	}
	if err := h.svc.UpdateGrant(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), groupID, req.Role, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已更新"})
}

// RevokeGrant DELETE /kbs/{kb}/group-roles/{group}（管理员/管理者）。
//
//	@Summary	撤销组授权（管理员/管理者，组员即时失访）
//	@Tags		kbs
//	@Produce	json
//	@Param		kb		path		int	true	"知识库 ID"
//	@Param		group	path		int	true	"组 ID"
//	@Success	200		{object}	object{message=string}
//	@Router		/kbs/{kb}/group-roles/{group} [delete]
//	@Security	BearerAuth
func (h *GroupHandler) RevokeGrant(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("group"), 10, 64)
	if err != nil || groupID <= 0 {
		httpx.FailWith(c, 404, httpx.CodeNotFound, "组授权不存在")
		return
	}
	if err := h.svc.RevokeGrant(c.Request.Context(), middleware.UserID(c), middleware.KBID(c), groupID, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "已撤销"})
}
