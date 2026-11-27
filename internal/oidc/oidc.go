// Package oidc 企业单点登录（FR-19）：OIDC 配置存取 + 一次性登录 code。
//
// 配置持久化在 settings 表（key=oidc_config，JSON），client_secret 经 AESGCM 加密落库。
// 一次性 code 是 IdP 回调与前端换 JWT 之间的短时凭证（60s、一次性），避免把
// access token 放进 URL。
package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrNotConfigured OIDC 未配置或未启用。
var ErrNotConfigured = errors.New("OIDC 未配置")

// Config OIDC 连接配置（settings 表 oidc_config 键，JSON 序列化存储）。
type Config struct {
	Enabled         bool     `json:"enabled"`
	Issuer          string   `json:"issuer"` // IdP 发现地址（https://host[/realm]）
	ClientID        string   `json:"client_id"`
	ClientSecretEnc string   `json:"client_secret_enc"` // AESGCM 加密后的 base64
	RedirectURI     string   `json:"redirect_uri"`      // 必须与 IdP 侧登记一致（/api/v1/auth/oidc/callback）
	FrontendURI     string   `json:"frontend_uri"`      // 登录完成后的前端地址（302 目标 origin）
	Scopes          []string `json:"scopes"`            // 默认 openid profile email
	GroupsClaim     string   `json:"groups_claim"`      // 组名数组所在 claim，默认 groups
	AdminClaim      string   `json:"admin_claim"`       // 可空=不映射管理员（FR-19 默认关闭）
	AdminClaimValue string   `json:"admin_claim_value"` // 该 claim 含此值 → is_admin
}

// Valid 配置是否完整可发起登录。
func (c *Config) Valid() bool {
	return c != nil && c.Enabled && c.Issuer != "" && c.ClientID != "" &&
		c.ClientSecretEnc != "" && c.RedirectURI != "" && c.FrontendURI != ""
}

// ScopesOrDefault 未配置 scopes 时的默认值。
func (c *Config) ScopesOrDefault() []string {
	if len(c.Scopes) > 0 {
		return c.Scopes
	}
	return []string{"openid", "profile", "email"}
}

// GroupsClaimOrDefault 未配置组 claim 时的默认值。
func (c *Config) GroupsClaimOrDefault() string {
	if c.GroupsClaim != "" {
		return c.GroupsClaim
	}
	return "groups"
}

// Secret 解密并返回 client_secret（未配置返回空串）。
func (c *Config) Secret(decrypt func([]byte) ([]byte, error)) (string, error) {
	if c.ClientSecretEnc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(c.ClientSecretEnc)
	if err != nil {
		return "", fmt.Errorf("client_secret 密文格式错误: %w", err)
	}
	plain, err := decrypt(raw)
	if err != nil {
		return "", fmt.Errorf("client_secret 解密失败: %w", err)
	}
	return string(plain), nil
}

// Store settings 存取（repo.SettingRepo 满足）。
type Store interface {
	GetString(ctx context.Context, key, def string) (string, error)
	Set(ctx context.Context, key string, value []byte, updatedBy int64) error
}

const settingKey = "oidc_config"

// SettingStore 基于 settings 表的 OIDC 配置存取（secret 加密委托 cipher）。
type SettingStore struct {
	Store   Store
	Encrypt func([]byte) ([]byte, error)
	Decrypt func([]byte) ([]byte, error)
}

// Load 读当前配置；未配置返回 ErrNotConfigured。
func (s *SettingStore) Load(ctx context.Context) (*Config, error) {
	raw, err := s.Store.GetString(ctx, settingKey, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, ErrNotConfigured
	}
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("OIDC 配置损坏: %w", err)
	}
	return &c, nil
}

// Save 写配置（newSecret 明文传入加密落库；空=不修改；调用方未携带旧密文时回填）。
func (s *SettingStore) Save(ctx context.Context, actorID int64, c *Config, newSecret string) error {
	if newSecret != "" {
		enc, err := s.Encrypt([]byte(newSecret))
		if err != nil {
			return fmt.Errorf("client_secret 加密失败: %w", err)
		}
		c.ClientSecretEnc = base64.StdEncoding.EncodeToString(enc)
	} else if c.ClientSecretEnc == "" {
		if old, lerr := s.Load(ctx); lerr == nil && old != nil {
			c.ClientSecretEnc = old.ClientSecretEnc
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.Store.Set(ctx, settingKey, raw, actorID)
}

// ---- 一次性登录 code（回调 → 前端换 JWT）----

type pendingLogin struct {
	UserID    int64
	ExpiresAt time.Time
}

// OneTimeCodes 一次性 code 存取（内存实现；60s TTL、消费即删）。
type OneTimeCodes struct {
	mu    sync.Mutex
	codes map[string]pendingLogin
	now   func() time.Time
	ttl   time.Duration
}

func NewOneTimeCodes() *OneTimeCodes {
	return &OneTimeCodes{codes: map[string]pendingLogin{}, now: time.Now, ttl: time.Minute}
}

// Issue 为用户签发一次性 code（登录完成 → 302 前端）。
func (o *OneTimeCodes) Issue(userID int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成一次性 code 失败: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(b)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.gcLocked()
	o.codes[code] = pendingLogin{UserID: userID, ExpiresAt: o.now().Add(o.ttl)}
	return code, nil
}

// Consume 消费 code（一次性；过期/不存在返回 false）。
func (o *OneTimeCodes) Consume(code string) (int64, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	p, ok := o.codes[code]
	if !ok {
		return 0, false
	}
	delete(o.codes, code)
	if o.now().After(p.ExpiresAt) {
		return 0, false
	}
	return p.UserID, true
}

func (o *OneTimeCodes) gcLocked() {
	now := o.now()
	for k, p := range o.codes {
		if now.After(p.ExpiresAt) {
			delete(o.codes, k)
		}
	}
}
