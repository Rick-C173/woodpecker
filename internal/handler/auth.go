package handler

import (
	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// AuthHandler 认证端点（FR-01）。
type AuthHandler struct {
	svc *service.AuthService
}

func NewAuthHandler(svc *service.AuthService) *AuthHandler { return &AuthHandler{svc: svc} }

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login POST /auth/login
//
//	@Summary	登录
//	@Description	登录，返回 JWT token 与 must_change_password / is_admin 标记。
//	@Description
//	@Description	连续 5 次密码错误锁定 10 分钟（AUTH_002）。首登未改密（must_change_password=true）时，除本接口与 /healthz 外所有接口返回 403 AUTH_07。
//	@Description
//	@Description	权限：公开。
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Success	200	{object}	service.AuthResult
//	@Failure	401	{object}	object{code=string,message=string,request_id=string}
//	@Router		/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeAuthInvalid, "用户名与密码不能为空")
		return
	}
	res, err := h.svc.Login(c.Request.Context(), req.Username, req.Password, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, res)
}

type registerReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Register POST /auth/register
//
//	@Summary	自助注册（首个用户自动成为管理员；默认关闭，见 FR-01）
//	@Description	自助注册（FR-01，默认关闭，系统设置 self_register 控制）。首个注册用户自动成为管理员。
//	@Description
//	@Description	错误：403 AUTH_005 自助注册未开放；409 USER_001 用户名已存在；422 AUTH_006 密码不符合规则（至少 8 位）。
//	@Description
//	@Description	权限：公开（设置开启时）。
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Success	201	{object}	service.AuthResult	"注册成功，直接返回登录态"
//	@Failure	403	{object}	httpx.ErrorBody	"注册未开放 AUTH_005"
//	@Failure	409	{object}	httpx.ErrorBody	"用户名已存在 USER_001"
//	@Failure	422	{object}	httpx.ErrorBody	"密码过短 AUTH_006"
//	@Router		/auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "用户名与密码不能为空")
		return
	}
	res, err := h.svc.Register(c.Request.Context(), req.Username, req.Password, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.Created(c, res)
}

type changePasswordReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

// ChangePassword POST /auth/password：成功后旧 token 全部失效，响应携带新 token。
//
//	@Summary	修改自己密码（含首登强制改密）
//	@Description	修改自己密码（含首登强制改密）。成功后 token_version+1，**所有旧 token 立即失效**，需重新登录。
//	@Description
//	@Description	must_change_password=true 时仅本接口可用（其余接口 403 AUTH_07）。
//	@Description
//	@Description	错误：401 原密码错误（AUTH_001）；422 AUTH_006 新密码不符合规则（至少 8 位）。
//	@Description
//	@Description	权限：登录。
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Security	ApiKeyAuth
//	@Success	200	{object}	service.AuthResult
//	@Failure	401	{object}	httpx.ErrorBody	"原密码错误 AUTH_001"
//	@Failure	422	{object}	httpx.ErrorBody	"新密码过短 AUTH_006"
//	@Router		/auth/password [post]
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req changePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "原密码与新密码不能为空")
		return
	}
	res, err := h.svc.ChangePassword(c.Request.Context(), middleware.UserID(c), req.OldPassword, req.NewPassword)
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, res)
}
