package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// TokenPrefix API Token 明文前缀：wpk_ + 64 hex。
// 与 httpx.TokenPrefix 保持同步（httpx 已引用本包，此处不能反向 import）。
const TokenPrefix = "wpk_"

// MaxTokensPerUser 每人有效 token 上限。
const MaxTokensPerUser = 20

// TokenLookupFn API Token 明文校验所需的存储能力（repo.APITokenRepo 满足）。
type TokenLookupFn interface {
	GetByHash(ctx context.Context, tokenHash string) (*model.APIToken, error)
	TouchLastUsed(ctx context.Context, id int64) error
}

// APITokenRepo service 层 token 管理所需的存储能力。
type APITokenRepo interface {
	TokenLookupFn
	CountByUser(ctx context.Context, userID int64) (int64, error)
	ListByUser(ctx context.Context, userID int64) ([]model.APIToken, error)
	Create(ctx context.Context, m *model.APIToken) error
	Revoke(ctx context.Context, id, userID int64) error
}

// APITokenService 个人 API Token（M15 / FR-20）：创建/列表/吊销/校验。
type APITokenService struct {
	tokens APITokenRepo
	audit  *AuditService
	now    func() time.Time
}

func NewAPITokenService(tokens APITokenRepo, audit *AuditService) *APITokenService {
	return &APITokenService{tokens: tokens, audit: audit, now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *APITokenService) SetClock(now func() time.Time) { s.now = now }

// APITokenItem token 列表项（不含哈希；token 明文永不回显）。
type APITokenItem struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"` // 展示用：客户端按 "wpk_"+prefix 拼接前 12 字符
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	CreatedAt  time.Time  `json:"created_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

// CreatedToken 创建成功响应：明文 token 仅本次返回，此后不可再取。
type CreatedToken struct {
	Item  APITokenItem `json:"item"`
	Token string       `json:"token"` // 完整明文 wpk_<64hex>，仅创建响应返回一次
}

// HashToken 计算完整明文（含 wpk_ 前缀）的 SHA-256 hex（小写）。
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// genPlainToken 生成明文 token：wpk_ + crypto/rand 32 字节 hex（64 字符，恒小写）。
func genPlainToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机数: %w", err)
	}
	return TokenPrefix + hex.EncodeToString(buf), nil
}

// Create 创建 token；plain 明文仅通过返回值交给 handler 一次性下发。
func (s *APITokenService) Create(ctx context.Context, userID int64, name string, expiresDays *int, ip string) (*CreatedToken, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, NewServiceError(CodeValidation, "名称不能为空且不超过 100 字符")
	}
	var expiresAt *time.Time
	if expiresDays != nil {
		if *expiresDays < 1 || *expiresDays > 3650 {
			return nil, NewServiceError(CodeValidation, "有效期须在 1~3650 天之间")
		}
		t := s.now().AddDate(0, 0, *expiresDays)
		expiresAt = &t
	}
	n, err := s.tokens.CountByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if n >= MaxTokensPerUser {
		return nil, NewServiceError(CodeTokenLimit, fmt.Sprintf("最多持有 %d 个有效 Token，请先吊销不再使用的", MaxTokensPerUser))
	}
	plain, err := genPlainToken()
	if err != nil {
		return nil, err
	}
	m := &model.APIToken{
		UserID:    userID,
		Name:      name,
		TokenHash: HashToken(plain),
		Prefix:    strings.TrimPrefix(plain, TokenPrefix)[:8],
		ExpiresAt: expiresAt,
	}
	if err := s.tokens.Create(ctx, m); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &userID, "token.create", "api_token", &m.ID,
		map[string]any{"name": name}, ip)
	return &CreatedToken{Item: itemOf(m), Token: plain}, nil
}

// List 用户 token 列表（含已吊销，便于展示历史）。
func (s *APITokenService) List(ctx context.Context, userID int64) ([]APITokenItem, error) {
	rows, err := s.tokens.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := make([]APITokenItem, 0, len(rows))
	for i := range rows {
		items = append(items, itemOf(&rows[i]))
	}
	return items, nil
}

// Revoke 吊销自己的 token；他人 token 与不存在统一 404（不泄露存在性）。
func (s *APITokenService) Revoke(ctx context.Context, userID, tokenID int64, ip string) error {
	if err := s.tokens.Revoke(ctx, tokenID, userID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "记录不存在")
		}
		return err
	}
	s.audit.Record(ctx, &userID, "token.revoke", "api_token", &tokenID, nil, ip)
	return nil
}

// Validate 供 Auth 中间件闭包调用：校验明文 token（含 wpk_ 前缀），返回所属用户 ID。
// 失配/已吊销/已过期统一 TOK_002；last_used_at 距上次 >1min 才刷新（best-effort，失败不阻断）。
func (s *APITokenService) Validate(ctx context.Context, plain string) (int64, error) {
	if !strings.HasPrefix(plain, TokenPrefix) {
		return 0, NewServiceError(CodeTokenInvalid, "API Token 无效")
	}
	m, err := s.tokens.GetByHash(ctx, HashToken(plain))
	if err != nil {
		return 0, NewServiceError(CodeTokenInvalid, "API Token 无效")
	}
	if m.RevokedAt != nil {
		return 0, NewServiceError(CodeTokenInvalid, "API Token 已吊销")
	}
	if m.ExpiresAt != nil && s.now().After(*m.ExpiresAt) {
		return 0, NewServiceError(CodeTokenInvalid, "API Token 已过期")
	}
	if m.LastUsedAt == nil || s.now().Sub(*m.LastUsedAt) > time.Minute {
		_ = s.tokens.TouchLastUsed(ctx, m.ID)
	}
	return m.UserID, nil
}

func itemOf(m *model.APIToken) APITokenItem {
	return APITokenItem{
		ID:         m.ID,
		Name:       m.Name,
		Prefix:     m.Prefix,
		LastUsedAt: m.LastUsedAt,
		ExpiresAt:  m.ExpiresAt,
		CreatedAt:  m.CreatedAt,
		RevokedAt:  m.RevokedAt,
	}
}
