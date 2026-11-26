package handler

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/mail"
	"woodpecker/internal/middleware"
	"woodpecker/internal/service"
)

// InviteHandler 邀请制注册（FR-16）：管理端邀请生命周期 + 公开校验/持邀注册 + SMTP 配置。
type InviteHandler struct {
	svc         *service.InviteService
	auth        *service.AuthService
	emailStore  *mail.SMTPSettingStore
	emailSender mail.Sender
}

func NewInviteHandler(svc *service.InviteService, auth *service.AuthService, emailStore *mail.SMTPSettingStore, emailSender mail.Sender) *InviteHandler {
	return &InviteHandler{svc: svc, auth: auth, emailStore: emailStore, emailSender: emailSender}
}

// CreateInvitations POST /admin/invitations（管理员）：单个/批量发起邀请。
//
//	@Summary	发起邀请（管理员）
//	@Description	邮箱小写化去重后逐个发送；已注册邮箱拒绝；SMTP 未配置或发送失败不阻断创建（invite.send_status 标记，降级返回明文链接）。
//	@Tags		invitations
//	@Accept		json
//	@Produce	json
//	@Param		body	body		object{emails=[]string}	true	"邮箱列表（单次 ≤50）"
//	@Success	200		{object}	object{results=[]service.InviteResult}
//	@Failure	422		{object}	httpx.ErrorBody
//	@Router		/admin/invitations [post]
//	@Security	BearerAuth
func (h *InviteHandler) CreateInvitations(c *gin.Context) {
	var req struct {
		Emails []string `json:"emails" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "emails 不能为空")
		return
	}
	results, err := h.svc.Create(c.Request.Context(), middleware.UserID(c), req.Emails, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"results": results})
}

// ListInvitations GET /admin/invitations（管理员）：邀请列表。
//
//	@Summary	邀请列表（管理员）
//	@Description	按状态/时间筛选，created_at 倒序分页。
//	@Tags		invitations
//	@Produce	json
//	@Param		status	query		string	false	"pending|accepted|revoked"
//	@Param		page	query		int		false	"页码（默认 1）"
//	@Param		page_size	query	int		false	"页大小（默认 20）"
//	@Success	200	{object}	object{items=[]service.InviteItem,total=int,page=int,page_size=int}
//	@Router		/admin/invitations [get]
//	@Security	BearerAuth
func (h *InviteHandler) ListInvitations(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	items, total, err := h.svc.List(c.Request.Context(), c.Query("status"), page, size)
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.PageOK(c, items, total, page, size)
}

// ResendInvitation POST /admin/invitations/{id}/resend（管理员）：重发邀请。
//
//	@Summary	重发邀请（管理员）
//	@Description	覆盖 token、重置 7 天有效期与发送状态；已接受邀请 409 AUTH_009。
//	@Tags		invitations
//	@Produce	json
//	@Param		id	path	int	true	"邀请 ID"
//	@Success	200	{object}	service.InviteItem
//	@Failure	409	{object}	httpx.ErrorBody
//	@Router		/admin/invitations/{id}/resend [post]
//	@Security	BearerAuth
func (h *InviteHandler) ResendInvitation(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, httpx.CodeNotFound, "邀请不存在")
		return
	}
	item, err := h.svc.Resend(c.Request.Context(), middleware.UserID(c), id, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, item)
}

// RevokeInvitation DELETE /admin/invitations/{id}（管理员）：撤销未接受邀请。
//
//	@Summary	撤销邀请（管理员）
//	@Description	已接受邀请不可撤销（409 AUTH_009）。
//	@Tags		invitations
//	@Produce	json
//	@Param		id	path	int	true	"邀请 ID"
//	@Success	200	{object}	object{message=string}
//	@Router		/admin/invitations/{id} [delete]
//	@Security	BearerAuth
func (h *InviteHandler) RevokeInvitation(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, httpx.CodeNotFound, "邀请不存在")
		return
	}
	if err := h.svc.Revoke(c.Request.Context(), middleware.UserID(c), id, c.ClientIP()); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "邀请已撤销"})
}

// ValidateInvitation GET /auth/invitations/{token}（公开）：注册页加载时校验邀请。
//
//	@Summary	邀请校验（公开）
//	@Description	返回邀请邮箱（只读展示）与过期时间；无效/过期 410 AUTH_008，已使用 409 AUTH_009。
//	@Tags		auth
//	@Produce	json
//	@Param		token	path	string	true	"邀请 token"
//	@Success	200	{object}	object{email=string,expires_at=string}
//	@Router		/auth/invitations/{token} [get]
func (h *InviteHandler) ValidateInvitation(c *gin.Context) {
	email, expiresAt, err := h.svc.Validate(c.Request.Context(), c.Param("token"))
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{
		"email":      email,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

// RegisterInvited POST /auth/register/invited（公开）：持邀注册。
//
//	@Summary	持邀注册（公开，FR-16）
//	@Description	token 一次性且 7 天有效；注册成功即作废邀请；一律普通用户（管理员由提升产生）。
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		body	body	object{token=string,username=string,password=string}	true	"邀请 token + 账号信息"
//	@Success	200	{object}	service.AuthResult
//	@Failure	410	{object}	httpx.ErrorBody
//	@Router		/auth/register/invited [post]
func (h *InviteHandler) RegisterInvited(c *gin.Context) {
	var req struct {
		Token    string `json:"token" binding:"required"`
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "token、用户名与密码不能为空")
		return
	}
	result, err := h.auth.RegisterInvited(c.Request.Context(), req.Token, req.Username, req.Password, c.ClientIP())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, result)
}

// GetEmailSettings GET /admin/email-settings（管理员）：SMTP 发件配置（密码不回显）。
//
//	@Summary	读取邮件配置（管理员）
//	@Tags		email
//	@Produce	json
//	@Success	200	{object}	mail.Config
//	@Router		/admin/email-settings [get]
//	@Security	BearerAuth
func (h *InviteHandler) GetEmailSettings(c *gin.Context) {
	cfg, err := h.emailStore.Load(c.Request.Context())
	if err != nil && !errors.Is(err, mail.ErrNotConfigured) {
		httpx.FailServiceError(c, err)
		return
	}
	if cfg == nil {
		httpx.OK(c, gin.H{"configured": false})
		return
	}
	cfg.PasswordEnc = "" // 密码不回显
	httpx.OK(c, cfg)
}

// emailSettingsUpdateRequest 配置保存请求（password 明文仅传入，加密落库；空=不修改）。
type emailSettingsUpdateRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}

// UpdateEmailSettings PATCH /admin/email-settings（管理员）：保存 SMTP 配置。
//
//	@Summary	保存邮件配置（管理员）
//	@Description	password 为空表示不修改现有密码；保存记审计。
//	@Tags		email
//	@Accept		json
//	@Produce	json
//	@Param		body	body	emailSettingsUpdateRequest	true	"SMTP 配置"
//	@Success	200	{object}	object{message=string}
//	@Router		/admin/email-settings [patch]
//	@Security	BearerAuth
func (h *InviteHandler) UpdateEmailSettings(c *gin.Context) {
	var req emailSettingsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "SMTP 配置格式错误")
		return
	}
	existing, _ := h.emailStore.Load(c.Request.Context())
	cfg := &mail.Config{
		Host:     req.Host,
		Port:     req.Port,
		Username: req.Username,
		From:     req.From,
	}
	if existing != nil && req.Password == "" {
		cfg.PasswordEnc = existing.PasswordEnc // 不修改密码
	}
	if err := h.emailStore.Save(c.Request.Context(), middleware.UserID(c), cfg, req.Password); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "邮件配置已保存"})
}

// TestEmail POST /admin/email-settings/test（管理员）：发送测试邮件。
//
//	@Summary	测试邮件发送（管理员）
//	@Tags		email
//	@Accept		json
//	@Produce	json
//	@Param		body	body	object{to=string}	true	"收件邮箱"
//	@Success	200	{object}	object{message=string}
//	@Failure	422	{object}	httpx.ErrorBody
//	@Router		/admin/email-settings/test [post]
//	@Security	BearerAuth
func (h *InviteHandler) TestEmail(c *gin.Context) {
	var req struct {
		To string `json:"to" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "收件邮箱不能为空")
		return
	}
	if err := h.emailSender.Send(c.Request.Context(), req.To,
		"Woodpecker 测试邮件", "这是一封测试邮件。收到即代表 SMTP 配置正确。"); err != nil {
		httpx.Fail(c, httpx.CodeEmailSendFail, "测试邮件发送失败: "+err.Error())
		return
	}
	httpx.OK(c, gin.H{"message": "测试邮件已发送，请查收"})
}
