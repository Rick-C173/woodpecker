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
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Success	200	{object}	service.AuthResult
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
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Security	ApiKeyAuth
//	@Success	200	{object}	service.AuthResult
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
