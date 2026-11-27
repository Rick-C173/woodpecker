package handler

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"woodpecker/internal/httpx"
	"woodpecker/internal/middleware"
	"woodpecker/internal/oidc"
	"woodpecker/internal/service"
)

// oidcFlowCookie 登录流中间态 cookie（state/nonce/PKCE verifier，10 分钟）。
const oidcFlowCookie = "oidc_flow"

type oidcFlowState struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
}

func randomToken64() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// OIDCHandler 企业单点登录（FR-19）：公开登录流 + 管理端配置。
type OIDCHandler struct {
	svc *service.OIDCService
}

func NewOIDCHandler(svc *service.OIDCService) *OIDCHandler {
	return &OIDCHandler{svc: svc}
}

// Status GET /auth/oidc/status（公开）。
//
//	@Summary	SSO 状态（公开）
//	@Description	`{enabled}`，登录页据此显隐「企业账号登录」。
//	@Tags		auth
//	@Produce	json
//	@Success	200	{object}	object{enabled=bool}
//	@Router		/auth/oidc/status [get]
func (h *OIDCHandler) Status(c *gin.Context) {
	enabled, err := h.svc.Status(c.Request.Context())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"enabled": enabled})
}

// Login GET /auth/oidc/login（公开）：302 至 IdP 授权端点。
//
//	@Summary	发起 SSO（公开）
//	@Description	state+nonce+PKCE verifier 存 HttpOnly cookie（10 分钟）；未启用 404。
//	@Tags		auth
//	@Success	302	{string}	string	"Location: IdP 授权端点"
//	@Failure	404	{object}	httpx.ErrorBody
//	@Router		/auth/oidc/login [get]
func (h *OIDCHandler) Login(c *gin.Context) {
	state := randomToken64()
	nonce := randomToken64()
	verifier := randomToken64()
	authURL, err := h.svc.AuthorizeURL(c.Request.Context(), state, nonce, verifier)
	if err != nil {
		if errors.Is(err, oidc.ErrNotConfigured) {
			httpx.FailWith(c, http.StatusNotFound, httpx.CodeNotFound, "SSO 未启用")
			return
		}
		httpx.FailServiceError(c, err)
		return
	}
	raw, _ := json.Marshal(oidcFlowState{State: state, Nonce: nonce, Verifier: verifier})
	c.SetCookie(oidcFlowCookie, base64.RawURLEncoding.EncodeToString(raw),
		600, "/api/v1/auth/oidc", "", secureCookie(c), true)
	c.Redirect(http.StatusFound, authURL)
}

// secureCookie HTTPS 部署下启用 Secure（本地 http 联调不启用）。
func secureCookie(c *gin.Context) bool { return c.Request.TLS != nil || c.Request.Host == "" }

// Callback GET /auth/oidc/callback（公开）。
//
//	@Summary	IdP 回调（公开）
//	@Description	校验 state/cookie → 换 token、JIT 建号、组同步 → 302 前端 `/?oidc=<一次性code>`；失败 302 `/?oidc_error=<原因>`。
//	@Tags		auth
//	@Success	302	{string}	string	"Location: 前端地址"
//	@Router		/auth/oidc/callback [get]
func (h *OIDCHandler) Callback(c *gin.Context) {
	frontend := h.frontendURI(c)
	fail := func(msg string) {
		c.Redirect(http.StatusFound, frontend+"/?oidc_error="+url.QueryEscape(msg))
	}

	raw, err := c.Cookie(oidcFlowCookie)
	if err != nil {
		fail("登录会话丢失，请重新发起登录")
		return
	}
	var flow oidcFlowState
	if b, derr := base64.RawURLEncoding.DecodeString(raw); derr != nil || json.Unmarshal(b, &flow) != nil {
		fail("登录会话损坏，请重新发起登录")
		return
	}
	if c.Query("state") != flow.State {
		fail("state 校验失败，请重新发起登录")
		return
	}
	code := c.Query("code")
	if code == "" {
		fail("IdP 未返回授权码：" + c.Query("error_description"))
		return
	}
	oneTime, err := h.svc.HandleCallback(c.Request.Context(), code, flow.Verifier, flow.Nonce, c.ClientIP())
	if err != nil {
		fail(err.Error())
		return
	}
	// 清理流程 cookie
	c.SetCookie(oidcFlowCookie, "", -1, "/api/v1/auth/oidc", "", secureCookie(c), true)
	c.Redirect(http.StatusFound, frontend+"/?oidc="+url.QueryEscape(oneTime))
}

// frontendURI 从配置读取前端地址；配置异常时兜底同源。
func (h *OIDCHandler) frontendURI(c *gin.Context) string {
	uri, err := h.svc.FrontendURI(c.Request.Context())
	if err != nil || uri == "" {
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		return scheme + "://" + c.Request.Host
	}
	return uri
}

// TokenExchange POST /auth/oidc/token（公开）：一次性 code 换系统 JWT。
//
//	@Summary	SSO 登录兑换（公开，FR-19）
//	@Description	`{code}` 一次性 60s；响应与密码登录同构 AuthResult。
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		body	body		object{code=string}	true	"一次性 code"
//	@Success	200		{object}	service.AuthResult
//	@Failure	401		{object}	httpx.ErrorBody	"AUTH_010 登录已过期"
//	@Router		/auth/oidc/token [post]
func (h *OIDCHandler) TokenExchange(c *gin.Context) {
	var req struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "code 不能为空")
		return
	}
	result, err := h.svc.ExchangeToken(c.Request.Context(), req.Code)
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, result)
}

// ---- 管理端配置（管理员）----

type oidcSettingsRequest struct {
	Enabled         bool     `json:"enabled"`
	Issuer          string   `json:"issuer"`
	ClientID        string   `json:"client_id"`
	ClientSecret    string   `json:"client_secret"` // 明文仅传入；空=不修改
	RedirectURI     string   `json:"redirect_uri"`
	FrontendURI     string   `json:"frontend_uri"`
	Scopes          []string `json:"scopes"`
	GroupsClaim     string   `json:"groups_claim"`
	AdminClaim      string   `json:"admin_claim"`
	AdminClaimValue string   `json:"admin_claim_value"`
}

// GetOidcSettings GET /admin/oidc-settings（管理员）：密码不回显。
//
//	@Summary	读取 OIDC 配置（管理员）
//	@Tags		oidc
//	@Produce	json
//	@Success	200	{object}	object{enabled=bool,issuer=string,client_id=string,redirect_uri=string,frontend_uri=string,scopes=[]string,groups_claim=string,admin_claim=string,admin_claim_value=string,configured=bool}
//	@Router		/admin/oidc-settings [get]
//	@Security	BearerAuth
func (h *OIDCHandler) GetOidcSettings(c *gin.Context) {
	cfg, err := h.svc.LoadConfig(c.Request.Context())
	if err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	if cfg == nil {
		httpx.OK(c, gin.H{"configured": false})
		return
	}
	cfg.ClientSecretEnc = "" // 不回显
	httpx.OK(c, cfg)
}

// PatchOidcSettings PATCH /admin/oidc-settings（管理员）。
//
//	@Summary	保存 OIDC 配置（管理员）
//	@Description	client_secret 空=不修改；保存记审计。
//	@Tags		oidc
//	@Accept		json
//	@Produce	json
//	@Param		body	body	oidcSettingsRequest	true	"OIDC 配置"
//	@Success	200		{object}	object{message=string}
//	@Router		/admin/oidc-settings [patch]
//	@Security	BearerAuth
func (h *OIDCHandler) PatchOidcSettings(c *gin.Context) {
	var req oidcSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.CodeValidation, "OIDC 配置格式错误")
		return
	}
	if err := h.svc.SaveConfig(c.Request.Context(), middleware.UserID(c), &service.OIDCConfigInput{
		Enabled:         req.Enabled,
		Issuer:          req.Issuer,
		ClientID:        req.ClientID,
		RedirectURI:     req.RedirectURI,
		FrontendURI:     req.FrontendURI,
		Scopes:          req.Scopes,
		GroupsClaim:     req.GroupsClaim,
		AdminClaim:      req.AdminClaim,
		AdminClaimValue: req.AdminClaimValue,
		NewSecret:       req.ClientSecret,
	}); err != nil {
		httpx.FailServiceError(c, err)
		return
	}
	httpx.OK(c, gin.H{"message": "OIDC 配置已保存"})
}

// TestOidcSettings POST /admin/oidc-settings/test（管理员）。
//
//	@Summary	OIDC 连接测试（管理员）
//	@Description	拉取 IdP 发现文档并报告可达性与关键端点。
//	@Tags		oidc
//	@Produce	json
//	@Success	200	{object}	object{issuer=string,authorization_endpoint=string,token_endpoint=string,userinfo_endpoint=string}
//	@Failure	422	{object}	httpx.ErrorBody
//	@Router		/admin/oidc-settings/test [post]
//	@Security	BearerAuth
func (h *OIDCHandler) TestOidcSettings(c *gin.Context) {
	result, err := h.svc.TestConnection(c.Request.Context())
	if err != nil {
		httpx.Fail(c, httpx.CodeValidation, "连接失败: "+err.Error())
		return
	}
	httpx.OK(c, result)
}
