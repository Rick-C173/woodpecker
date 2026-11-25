package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
)

// AdminUsersHandler 管理员用户管理端点（FR-13）。
type AdminUsersHandler struct {
	svc *service.UserAdminService
}

func NewAdminUsersHandler(svc *service.UserAdminService) *AdminUsersHandler {
	return &AdminUsersHandler{svc: svc}
}

// List GET /admin/users
//
//	@Summary	用户列表
//	@Description	用户列表（分页，按创建时间排序）。status: 1=启用 2=禁用；含 must_change_password 与 kb_count（所属库数量）。
//	@Description
//	@Description	权限：管理员（普通用户 403）。
//	@Tags		admin-users
//	@Produce	json
//	@Param		q		query		string	false	"用户名关键词"
//	@Param		status	query		int		false	"1=启用 2=禁用"
//	@Param		page	query		int		false	"页码"
//	@Success	200		{object}	object{items=[]service.AdminUserItem,total=int,page=int,page_size=int}
//	@Param	page_size	query	integer	false	"每页数量，1-100；小于 1、大于 100 或非法值归一化为 20"
//	@Failure	403	{object}	httpx.ErrorBody	"非管理员 FORBIDDEN"
//	@Router		/admin/users [get]
func (h *AdminUsersHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	status, _ := strconv.Atoi(c.Query("status"))
	items, total, err := h.svc.List(c.Request.Context(), repo.UserListQuery{
		Keyword: c.Query("q"),
		Status:  int16(status),
		Page:    page,
		PerPage: perPage,
	})
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.PageOK(c, items, total, page, perPage)
}

type createUserReq struct {
	Username string `json:"username" binding:"required"` // 用户名（唯一）
	Password string `json:"password"`                    // 初始密码，缺省则随机生成（≥8 位，AUTH_006）
	IsAdmin  bool   `json:"is_admin"`                    // 是否管理员
}

type createUserResp struct {
	ID                 int64  `json:"id"`                   // 新用户 ID
	Username           string `json:"username"`             // 用户名
	IsAdmin            bool   `json:"is_admin"`             // 是否管理员
	InitialPassword    string `json:"initial_password"`     // 一次性初始密码，仅本次返回，不落明文
	MustChangePassword bool   `json:"must_change_password"` // 恒为 true：首登强制改密
}

// Create POST /admin/users
//
//	@Summary	创建用户（返回一次性初始密码）
//	@Description	创建用户，响应含**一次性初始密码**（仅此一次展示，不落明文）。新用户 must_change_password=true，首登强制改密。
//	@Description
//	@Description	错误：409 USER_001 用户名已存在；422 AUTH_006 初始密码不符合规则。
//	@Description
//	@Description	权限：管理员。
//	@Tags		admin-users
//	@Accept		json
//	@Produce	json
//	@Success	201	{object}	createUserResp
//	@Failure	409	{object}	httpx.ErrorBody	"重名 USER_001"
//	@Failure	422	{object}	httpx.ErrorBody	"VALIDATION_FAILED / AUTH_006"
//	@Router		/admin/users [post]
func (h *AdminUsersHandler) Create(c *gin.Context) {
	var req createUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "用户名与密码不能为空")
		return
	}
	initial, u, err := h.svc.Create(c.Request.Context(), middleware.UserID(c), req.Username, req.Password, req.IsAdmin, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, createUserResp{
		ID:                 u.ID,
		Username:           u.Username,
		IsAdmin:            u.IsAdmin,
		InitialPassword:    initial,
		MustChangePassword: u.MustChangePassword,
	})
}

type patchUserReq struct {
	Status  *int16 `json:"status"`   // 1=启用 2=禁用（禁用自己 → USER_002）
	IsAdmin *bool  `json:"is_admin"` // 撤最后一个管理员 → USER_003
}

type patchUserResp struct {
	ID      int64  `json:"id"`      // 用户 ID
	Message string `json:"message"` // 结果说明
}

// Patch PATCH /admin/users/{id}：禁用/启用、设/撤管理员
//
//	@Summary	禁用/启用、设/撤管理员（USER_002/003 保护）
//	@Description	禁用/启用账号、设/撤管理员（字段缺省不修改）。
//	@Description
//	@Description	保护规则：禁用自己 → 422 USER_002；撤销最后一个管理员 → 422 USER_003。权限变更（禁用/撤管理员）后旧 token 立即失效（token_version 不符 → 401 AUTH_004）。
//	@Description
//	@Description	权限：管理员。
//	@Tags		admin-users
//	@Accept		json
//	@Produce	json
//	@Success	200	{object}	patchUserResp
//	@Param	id	path	integer	true	"ID"
//	@Failure	404	{object}	httpx.ErrorBody	"用户不存在 NOT_FOUND"
//	@Failure	422	{object}	httpx.ErrorBody	"USER_002/USER_003/VALIDATION_FAILED"
//	@Router		/admin/users/{id} [patch]
func (h *AdminUsersHandler) Patch(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.FailWith(c, http.StatusNotFound, "NOT_FOUND", "用户不存在")
		return
	}
	var req patchUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "请求体非法")
		return
	}
	actor := middleware.UserID(c)
	ip := c.ClientIP()
	if req.Status != nil {
		if *req.Status != 1 && *req.Status != 2 {
			httpx.Fail(c, httpx.CodeValidation, "status 取值仅 1=启用 2=禁用")
			return
		}
		if err := h.svc.SetStatus(c.Request.Context(), actor, id, *req.Status == 2, ip); err != nil {
			httpx.FailServiceError(c, err)
			return
		}
	}
	if req.IsAdmin != nil {
		if err := h.svc.SetAdmin(c.Request.Context(), actor, id, *req.IsAdmin, ip); err != nil {
			httpx.FailServiceError(c, err)
			return
		}
	}
	httpx.OK(c, patchUserResp{ID: id, Message: "已更新"})
}

type resetPasswordResp struct {
	InitialPassword string `json:"initial_password"` // 一次性初始密码，仅本次返回
}

// ResetPassword POST /admin/users/{id}/reset-password
//
//	@Summary	重置密码（返回一次性初始密码，用户下次登录强制修改）
//	@Description	重置用户密码，响应含**一次性初始密码**。重置后 must_change_password=true，旧 token 立即失效。
//	@Description
//	@Description	权限：管理员。
//	@Tags		admin-users
//	@Produce	json
//	@Success	200	{object}	resetPasswordResp
//	@Param	id	path	integer	true	"ID"
//	@Failure	404	{object}	httpx.ErrorBody	"用户不存在 NOT_FOUND"
//	@Router		/admin/users/{id}/reset-password [post]
func (h *AdminUsersHandler) ResetPassword(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.FailWith(c, http.StatusNotFound, "NOT_FOUND", "用户不存在")
		return
	}
	initial, err := h.svc.ResetPassword(c.Request.Context(), middleware.UserID(c), id, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, resetPasswordResp{InitialPassword: initial})
}
