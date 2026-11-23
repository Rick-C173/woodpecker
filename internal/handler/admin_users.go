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
//	@Tags		admin-users
//	@Produce	json
//	@Param		q		query		string	false	"用户名关键词"
//	@Param		status	query		int		false	"1=启用 2=禁用"
//	@Param		page	query		int		false	"页码"
//	@Success	200		{object}	object{items=[]service.AdminUserItem,total=int,page=int,page_size=int}
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
	Username string `json:"username" binding:"required"`
	Password string `json:"password"` // 缺省则随机生成
	IsAdmin  bool   `json:"is_admin"`
}

type createUserResp struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	IsAdmin            bool   `json:"is_admin"`
	InitialPassword    string `json:"initial_password"` // 仅此一次返回
	MustChangePassword bool   `json:"must_change_password"`
}

// Create POST /admin/users
//
//	@Summary	创建用户（返回一次性初始密码）
//	@Tags		admin-users
//	@Accept		json
//	@Produce	json
//	@Success	201	{object}	createUserResp
//	@Router		/admin/users [post]
func (h *AdminUsersHandler) Create(c *gin.Context) {
	var req createUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeAuthInvalid, "用户名不能为空")
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
	Status  *int16 `json:"status"`  // 1=启用 2=禁用
	IsAdmin *bool  `json:"is_admin"`
}

type patchUserResp struct {
	ID      int64  `json:"id"`
	Message string `json:"message"`
}

// Patch PATCH /admin/users/{id}：禁用/启用、设/撤管理员
//
//	@Summary	禁用/启用、设/撤管理员（USER_002/003 保护）
//	@Tags		admin-users
//	@Accept		json
//	@Produce	json
//	@Success	200	{object}	patchUserResp
//	@Router		/admin/users/{id} [patch]
func (h *AdminUsersHandler) Patch(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.FailWith(c, http.StatusNotFound, "NOT_FOUND", "用户不存在")
		return
	}
	var req patchUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeAuthInvalid, "请求体非法")
		return
	}
	actor := middleware.UserID(c)
	ip := c.ClientIP()
	if req.Status != nil {
		if *req.Status != 1 && *req.Status != 2 {
			httpx.Fail(c, httpx.CodeAuthInvalid, "status 取值仅 1=启用 2=禁用")
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
	InitialPassword string `json:"initial_password"` // 仅此一次返回
}

// ResetPassword POST /admin/users/{id}/reset-password
//
//	@Summary	重置密码（返回一次性初始密码，用户下次登录强制修改）
//	@Tags		admin-users
//	@Produce	json
//	@Success	200	{object}	resetPasswordResp
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
