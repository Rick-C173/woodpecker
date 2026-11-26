package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	foidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"woodpecker/internal/crypto"
	"woodpecker/internal/jwt"
	"woodpecker/internal/model"
	"woodpecker/internal/oidc"
	"woodpecker/internal/repo"
)

// OIDC 本地错误码副本（service 层不 import httpx）。
const (
	CodeOIDCLoginFail = "AUTH_010" // 401 SSO 登录失败（IdP 不可达/回调非法/账号被禁用）
)

// OIDCUserStore 用户存取（repo.UserRepo 满足；FR-19 JIT 建号/绑定）。
type OIDCUserStore interface {
	GetByEmail(ctx context.Context, email string) (*model.User, error)
	GetByUsername(ctx context.Context, username string) (*model.User, error)
	GetByID(ctx context.Context, id int64) (*model.User, error)
	Create(ctx context.Context, u *model.User) error
	Save(ctx context.Context, u *model.User) error
}

// OIDCGroupStore 组存取（repo.GroupRepo 满足；FR-19 组自动同步）。
type OIDCGroupStore interface {
	GetByName(ctx context.Context, name string) (*model.UserGroup, error)
	Create(ctx context.Context, g *model.UserGroup) error
	GetMemberByID(ctx context.Context, groupID, userID int64) (*model.GroupMember, error)
	AddMember(ctx context.Context, groupID, userID int64) error
	RemoveMember(ctx context.Context, groupID, userID int64) error
	ListAutoGroupsByUser(ctx context.Context, userID int64) ([]model.UserGroup, error)
}

// OIDCService 企业单点登录（FR-19）：授权 URL 构造、回调处理（JIT + 组同步）、配置管理。
type OIDCService struct {
	cfg    *oidc.SettingStore
	users  OIDCUserStore
	groups OIDCGroupStore
	issuer *jwt.Issuer
	codes  *oidc.OneTimeCodes
	audit  *AuditService
	now    func() time.Time
}

func NewOIDCService(cfg *oidc.SettingStore, users OIDCUserStore, groups OIDCGroupStore, issuer *jwt.Issuer, audit *AuditService) *OIDCService {
	return &OIDCService{cfg: cfg, users: users, groups: groups, issuer: issuer, codes: oidc.NewOneTimeCodes(), audit: audit, now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *OIDCService) SetClock(now func() time.Time) { s.now = now }

// Status SSO 是否启用（公开端点：登录页按钮显隐）。
func (s *OIDCService) Status(ctx context.Context) (bool, error) {
	c, err := s.cfg.Load(ctx)
	if err != nil {
		if errors.Is(err, oidc.ErrNotConfigured) {
			return false, nil
		}
		return false, err
	}
	return c.Enabled && c.Valid(), nil
}

// FrontendURI 登录完成后的前端地址（回调 302 目标 origin）。
func (s *OIDCService) FrontendURI(ctx context.Context) (string, error) {
	c, err := s.cfg.Load(ctx)
	if err != nil {
		return "", err
	}
	return c.FrontendURI, nil
}

// LoadConfig 管理页读取（未配置返回 nil）。
func (s *OIDCService) LoadConfig(ctx context.Context) (*oidc.Config, error) {
	c, err := s.cfg.Load(ctx)
	if errors.Is(err, oidc.ErrNotConfigured) {
		return nil, nil
	}
	return c, err
}

// OIDCConfigInput 管理页保存请求（handler 层转换；NewSecret 明文仅内存）。
type OIDCConfigInput struct {
	Enabled         bool
	Issuer          string
	ClientID        string
	RedirectURI     string
	FrontendURI     string
	Scopes          []string
	GroupsClaim     string
	AdminClaim      string
	AdminClaimValue string
	NewSecret       string
}

// SaveConfig 保存 OIDC 配置（NewSecret 空=不修改；secret 加密落库；记审计）。
func (s *OIDCService) SaveConfig(ctx context.Context, actorID int64, in *OIDCConfigInput) error {
	c, err := s.cfg.Load(ctx)
	if errors.Is(err, oidc.ErrNotConfigured) {
		c = &oidc.Config{}
	} else if err != nil {
		return err
	}
	c.Enabled = in.Enabled
	c.Issuer = strings.TrimSuffix(strings.TrimSpace(in.Issuer), "/")
	c.ClientID = strings.TrimSpace(in.ClientID)
	c.RedirectURI = strings.TrimSpace(in.RedirectURI)
	c.FrontendURI = strings.TrimSuffix(strings.TrimSpace(in.FrontendURI), "/")
	c.Scopes = in.Scopes
	c.GroupsClaim = strings.TrimSpace(in.GroupsClaim)
	c.AdminClaim = strings.TrimSpace(in.AdminClaim)
	c.AdminClaimValue = strings.TrimSpace(in.AdminClaimValue)
	if c.Issuer == "" || c.ClientID == "" || c.RedirectURI == "" || c.FrontendURI == "" {
		return NewServiceError(CodeValidation, "issuer、client_id、redirect_uri 与 frontend_uri 均必填")
	}
	if err := s.cfg.Save(ctx, actorID, c, in.NewSecret); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "oidc.config_update", "settings", nil,
		map[string]any{"enabled": c.Enabled, "issuer": c.Issuer}, "")
	return nil
}

func (s *OIDCService) oauth2Config(ctx context.Context) (conf *oauth2.Config, provider *foidc.Provider, vCfg *oidc.Config, err error) {
	c, err := s.cfg.Load(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if !c.Valid() {
		return nil, nil, nil, oidc.ErrNotConfigured
	}
	secret, err := c.Secret(s.cfg.Decrypt)
	if err != nil {
		return nil, nil, nil, err
	}
	provider, err = foidc.NewProvider(ctx, c.Issuer) // 拉取 .well-known/openid-configuration
	if err != nil {
		return nil, nil, nil, fmt.Errorf("IdP 发现端点不可达: %w", err)
	}
	conf = &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: secret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  c.RedirectURI,
		Scopes:       c.ScopesOrDefault(),
	}
	return conf, provider, c, nil
}

// AuthorizeURL 构造 IdP 授权跳转 URL（PKCE + nonce；state/cookie 校验由 handler 负责）。
func (s *OIDCService) AuthorizeURL(ctx context.Context, state, nonce, verifier string) (string, error) {
	conf, _, _, err := s.oauth2Config(ctx)
	if err != nil {
		return "", err
	}
	return conf.AuthCodeURL(state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.S256ChallengeOption(verifier),
	), nil
}

// oidcClaims IdP 身份 claim（userinfo 原始 JSON，组/管理员 claim 均从中提取）。
type oidcClaims struct {
	Email             string
	PreferredUsername string
	Groups            []string
	Raw               map[string]any
}

// HandleCallback IdP 回调处理：换 token → 校验 nonce → JIT 建号/绑定 → 组同步 → 一次性 code。
func (s *OIDCService) HandleCallback(ctx context.Context, code, verifier, nonce, ip string) (string, error) {
	conf, provider, cfg, err := s.oauth2Config(ctx)
	if err != nil {
		return "", err
	}
	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", fmt.Errorf("授权码兑换失败: %w", err)
	}
	// ID token 验签 + nonce 校验（issuer/client 已由 Verifier 校验）
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return "", errors.New("IdP 未返回 id_token")
	}
	ver := provider.Verifier(&foidc.Config{ClientID: cfg.ClientID})
	idToken, err := ver.Verify(ctx, rawID)
	if err != nil {
		return "", fmt.Errorf("id_token 校验失败: %w", err)
	}
	if nonce != "" && idToken.Nonce != nonce {
		return "", errors.New("nonce 不匹配")
	}

	// 身份 claim：userinfo 优先（groups 多在 userinfo）
	claims, err := s.fetchClaims(ctx, provider, tok, cfg)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(claims.Email) == "" {
		return "", errors.New("IdP 未返回 email，无法确定账号")
	}

	userID, err := s.upsertOIDCUser(ctx, claims, cfg)
	if err != nil {
		return "", err
	}
	added, removed := s.syncGroups(ctx, userID, claims.Groups)
	if len(added)+len(removed) > 0 {
		s.audit.Record(ctx, &userID, "oidc.group_sync", "user", &userID,
			map[string]any{"added": added, "removed": removed}, ip)
	}
	return s.codes.Issue(userID)
}

// fetchClaims 拉取 userinfo 并按配置提取身份/组/管理员 claim。
func (s *OIDCService) fetchClaims(ctx context.Context, provider *foidc.Provider, tok *oauth2.Token, cfg *oidc.Config) (*oidcClaims, error) {
	info, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(tok))
	if err != nil {
		return nil, fmt.Errorf("userinfo 获取失败: %w", err)
	}
	var raw map[string]any
	if err := info.Claims(&raw); err != nil {
		return nil, fmt.Errorf("userinfo 解析失败: %w", err)
	}
	c := &oidcClaims{Groups: []string{}, Raw: raw}
	if v, ok := raw["email"].(string); ok {
		c.Email = v
	}
	if v, ok := raw["preferred_username"].(string); ok {
		c.PreferredUsername = v
	}
	c.Groups = ExtractGroups(raw, cfg.GroupsClaimOrDefault())
	return c, nil
}

// ExtractGroups 按配置的组 claim 从原始 userinfo JSON 中提取组名数组（公共给 handler/测试）。
func ExtractGroups(raw map[string]any, claim string) []string {
	out := []string{}
	v, ok := raw[claim]
	if !ok || v == nil {
		return out
	}
	switch arr := v.(type) {
	case []any:
		for _, item := range arr {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = append(out, arr...)
	case string:
		if arr != "" {
			out = append(out, arr)
		}
	}
	return out
}

// usernamePattern 组名/用户名净化：保留字母数字 ._ -，其余替换为 -。
var usernameSanitize = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// sanitizeUsername IdP 用户名 → 本系统用户名（小写、合法字符、≤64）。
func sanitizeUsername(raw string) string {
	s := usernameSanitize.ReplaceAllString(strings.TrimSpace(strings.ToLower(raw)), "-")
	s = strings.Trim(s, "-._")
	if s == "" || utf8.RuneCountInString(s) > 64 {
		s = "oidc-" + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprint(time.Now().UnixNano())))[:8]
	}
	return s
}

// upsertOIDCUser 身份锚点=email：已有账号绑定（不改密码/角色，禁用拒绝）；
// 不存在则 JIT 建号（auth_source=oidc、无本地密码、普通用户/管理员映射）。
func (s *OIDCService) upsertOIDCUser(ctx context.Context, claims *oidcClaims, cfg *oidc.Config) (int64, error) {
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	u, err := s.users.GetByEmail(ctx, email)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return 0, err
	}
	if err == nil {
		// 绑定既有账号（不改密码/角色；管理员映射仅对 JIT 新用户生效，存量由管理端调整）
		if u.Status != 1 {
			return 0, NewServiceError(CodeAuthDisabled, "账号已禁用，请联系管理员")
		}
		return u.ID, nil
	}

	// JIT 建号
	base := sanitizeUsername(claims.PreferredUsername)
	if base == "" {
		base = sanitizeUsername(email)
	}
	username := base
	for i := 2; ; i++ {
		_, gerr := s.users.GetByUsername(ctx, username)
		if errors.Is(gerr, repo.ErrNotFound) {
			break
		}
		if gerr != nil {
			return 0, gerr
		}
		if i > 50 {
			return 0, errors.New("无法生成唯一用户名")
		}
		username = fmt.Sprintf("%s-%d", base, i)
	}
	isAdmin := cfg.AdminClaim != "" && cfg.AdminClaimValue != "" && claimsHasAdmin(claims, cfg)
	hash, err := bcryptRandom()
	if err != nil {
		return 0, err
	}
	nu := &model.User{
		Username:           username,
		PasswordHash:       hash, // 随机不可知 → 本地无法密码登录
		IsAdmin:            isAdmin,
		Status:             1,
		MustChangePassword: false,
		Email:              &email,
		AuthSource:         model.AuthSourceOIDC,
	}
	if err := s.users.Create(ctx, nu); err != nil {
		return 0, err
	}
	s.audit.Record(ctx, &nu.ID, "oidc.user_provisioned", "user", &nu.ID,
		map[string]any{"username": username, "email": email, "is_admin": isAdmin}, "")
	return nu.ID, nil
}

// claimsHasAdmin 判定管理员映射 claim 是否命中（从 userinfo 原始 JSON）。
func claimsHasAdmin(claims *oidcClaims, cfg *oidc.Config) bool {
	for _, item := range ExtractGroups(claims.Raw, cfg.AdminClaim) {
		if item == cfg.AdminClaimValue {
			return true
		}
	}
	return false
}

// syncGroups 组自动同步（FR-19）：双向对齐该用户的自动组（auto=true）；
// claim 中的组不存在则自动建组并加入；自动组不在 claim 中则移出；手动组不碰。
func (s *OIDCService) syncGroups(ctx context.Context, userID int64, claimGroups []string) (added, removed []string) {
	claim := map[string]struct{}{}
	for _, g := range claimGroups {
		name := strings.TrimSpace(g)
		if name == "" || utf8.RuneCountInString(name) > groupNameMax {
			continue
		}
		claim[name] = struct{}{}
	}
	// 1. claim → 加入（自动建组）
	for name := range claim {
		g, err := s.groups.GetByName(ctx, name)
		if errors.Is(err, repo.ErrNotFound) {
			ng := &model.UserGroup{Name: name, Description: "OIDC 组同步自动创建", Auto: true, CreatedBy: 0}
			if cerr := s.groups.Create(ctx, ng); cerr != nil {
				continue
			}
			g = ng
		} else if err != nil || g == nil {
			continue
		}
		if _, err := s.groups.GetMemberByID(ctx, g.ID, userID); errors.Is(err, repo.ErrNotFound) {
			if aerr := s.groups.AddMember(ctx, g.ID, userID); aerr == nil {
				added = append(added, name)
			}
		}
	}
	// 2. 该用户所在自动组 ∉ claim → 移出
	autoGroups, err := s.groups.ListAutoGroupsByUser(ctx, userID)
	if err != nil {
		return added, removed
	}
	for _, g := range autoGroups {
		if _, inClaim := claim[g.Name]; !inClaim {
			if rerr := s.groups.RemoveMember(ctx, g.ID, userID); rerr == nil {
				removed = append(removed, g.Name)
			}
		}
	}
	return added, removed
}

// ExchangeToken 前端用一次性 code 换系统 JWT（与密码登录同构 AuthResult）。
func (s *OIDCService) ExchangeToken(ctx context.Context, code string) (*AuthResult, error) {
	userID, ok := s.codes.Consume(code)
	if !ok {
		return nil, NewServiceError(CodeOIDCLoginFail, "登录已过期，请重新登录")
	}
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, NewServiceError(CodeOIDCLoginFail, "登录已过期，请重新登录")
	}
	if u.Status != 1 {
		return nil, NewServiceError(CodeAuthDisabled, "账号已禁用，请联系管理员")
	}
	token, err := s.issuer.Issue(u)
	if err != nil {
		return nil, err
	}
	return &AuthResult{
		Token:              token,
		UserID:             u.ID,
		Username:           u.Username,
		IsAdmin:            u.IsAdmin,
		MustChangePassword: u.MustChangePassword,
	}, nil
}

// bcryptRandom 生成随机不可登录的密码哈希（JIT 账号无本地密码）。
func bcryptRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return crypto.HashPassword(base64.RawURLEncoding.EncodeToString(b))
}

// TestConnection 连接测试（管理页）：拉取 IdP 发现文档并报告关键端点。
func (s *OIDCService) TestConnection(ctx context.Context) (map[string]any, error) {
	c, err := s.cfg.Load(ctx)
	if err != nil {
		return nil, err
	}
	provider, err := foidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, fmt.Errorf("IdP 发现端点不可达: %w", err)
	}
	var doc struct {
		Issuer      string `json:"issuer"`
		AuthURL     string `json:"authorization_endpoint"`
		TokenURL    string `json:"token_endpoint"`
		UserInfoURL string `json:"userinfo_endpoint"`
	}
	if err := provider.Claims(&doc); err != nil {
		return nil, err
	}
	return map[string]any{
		"issuer":                 doc.Issuer,
		"authorization_endpoint": doc.AuthURL,
		"token_endpoint":         doc.TokenURL,
		"userinfo_endpoint":      doc.UserInfoURL,
	}, nil
}
