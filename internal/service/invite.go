package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"woodpecker/internal/mail"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// 邀请参数（FR-16）。
const (
	inviteTokenTTL = 7 * 24 * time.Hour
	inviteEmailMax = 255
	inviteBatchMax = 50
)

// emailPattern 简单格式校验（本地@域名，域名含点）。
var emailPattern = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// InviteRepo 邀请存储接口（repo.InviteRepo 满足）。
type InviteRepo interface {
	GetByEmail(ctx context.Context, email string) (*model.Invite, error)
	GetByTokenHash(ctx context.Context, tokenHash string) (*model.Invite, error)
	GetByID(ctx context.Context, id int64) (*model.Invite, error)
	Upsert(ctx context.Context, m *model.Invite) error
	MarkSent(ctx context.Context, id int64, sendStatus int16, sendErr string) error
	Revoke(ctx context.Context, id int64) error
	List(ctx context.Context, q repo.InviteListQuery) ([]model.Invite, int64, error)
}

// InviteUserLookup 邮箱占用检查（users.email）。
type InviteUserLookup interface {
	CountByEmail(ctx context.Context, email string) (int64, error)
}

// InviteService 邀请生命周期（FR-16）：创建/发送/列表/重发/撤销/校验。
// 持邀注册（创建用户）在 AuthService.RegisterInvited。
type InviteService struct {
	invites InviteRepo
	users   InviteUserLookup
	sender  mail.Sender
	smtp    *mail.SMTPSettingStore // SMTP 配置存取（nil=视为未配置）
	baseURL string                 // 站点外部地址（邀请链接前缀，部署属性 APP_BASE_URL）
	audit   *AuditService
	now     func() time.Time
}

func NewInviteService(invites InviteRepo, users InviteUserLookup, sender mail.Sender, smtp *mail.SMTPSettingStore, baseURL string, audit *AuditService) *InviteService {
	return &InviteService{invites: invites, users: users, sender: sender, smtp: smtp, baseURL: strings.TrimSuffix(baseURL, "/"), audit: audit, now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *InviteService) SetClock(now func() time.Time) { s.now = now }

// InviteItem 邀请列表/操作响应。
type InviteItem struct {
	ID         int64  `json:"id"`
	Email      string `json:"email"`
	Status     string `json:"status"`      // pending | accepted | revoked
	SendStatus string `json:"send_status"` // sent | failed | not_sent
	SendError  string `json:"send_error,omitempty"`
	ExpiresAt  string `json:"expires_at"`
	CreatedBy  int64  `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	Link       string `json:"link,omitempty"` // 仅创建/重发响应携带（邮件丢失时手动转发）；列表为空
}

func inviteStatusName(s int16) string {
	switch s {
	case model.InviteStatusAccepted:
		return "accepted"
	case model.InviteStatusRevoked:
		return "revoked"
	default:
		return "pending"
	}
}

func sendStatusName(s int16) string {
	switch s {
	case model.InviteSentFailed:
		return "failed"
	case 1:
		return "sent"
	default:
		return "not_sent"
	}
}

func (s *InviteService) toItem(m *model.Invite) *InviteItem {
	return &InviteItem{
		ID:         m.ID,
		Email:      m.Email,
		Status:     inviteStatusName(m.Status),
		SendStatus: sendStatusName(m.SendStatus),
		ExpiresAt:  m.ExpiresAt.UTC().Format(time.RFC3339),
		CreatedBy:  m.CreatedBy,
		CreatedAt:  m.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// InviteResult 批量邀请的逐邮箱结果。
type InviteResult struct {
	Email  string      `json:"email"`
	OK     bool        `json:"ok"`
	Sent   bool        `json:"sent"` // 邮件是否已发出（false=SMTP 未配置/发送失败，链接可手动转发）
	Invite *InviteItem `json:"invite,omitempty"`
	Link   string      `json:"link,omitempty"` // 降级路径：未发出时返回明文链接
	Error  string      `json:"error,omitempty"`
}

// CreateRequest 发起邀请（单个或批量）。
type CreateRequest struct {
	Emails []string `json:"emails"`
}

// Create 校验/去重/落库并发送邀请邮件。SMTP 未配置或发送失败不阻断创建
// （降级：返回明文链接供管理员手动转发，EMAIL_001 仅在全部失败时作为整体提示语义）。
func (s *InviteService) Create(ctx context.Context, actorID int64, emails []string, ip string) ([]InviteResult, error) {
	normalized := make([]string, 0, len(emails))
	seen := map[string]struct{}{}
	for _, raw := range emails {
		e := strings.ToLower(strings.TrimSpace(raw))
		if e == "" {
			continue
		}
		if len(e) > inviteEmailMax || !emailPattern.MatchString(e) {
			return nil, NewServiceError(CodeValidation, fmt.Sprintf("邮箱格式不正确: %s", raw))
		}
		if _, dup := seen[e]; !dup {
			seen[e] = struct{}{}
			normalized = append(normalized, e)
		}
	}
	if len(normalized) == 0 {
		return nil, NewServiceError(CodeValidation, "请提供至少一个有效邮箱")
	}
	if len(normalized) > inviteBatchMax {
		return nil, NewServiceError(CodeValidation, fmt.Sprintf("单次最多邀请 %d 个邮箱", inviteBatchMax))
	}

	results := make([]InviteResult, 0, len(normalized))
	for _, email := range normalized {
		res := s.inviteOne(ctx, actorID, email, ip)
		results = append(results, res)
	}
	s.audit.Record(ctx, &actorID, "invite.create", "user", nil,
		map[string]any{"count": len(results)}, ip)
	return results, nil
}

// inviteOne 单个邮箱的邀请创建与发送（审计在 Create 层统一记录）。
func (s *InviteService) inviteOne(ctx context.Context, actorID int64, email, _ string) InviteResult {
	res := InviteResult{Email: email}
	n, err := s.users.CountByEmail(ctx, email)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if n > 0 {
		res.Error = "该邮箱已注册"
		return res
	}

	token, err := randomToken()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	m := &model.Invite{
		Email:     email,
		TokenHash: mail.HashToken(token),
		Status:    model.InviteStatusPending,
		ExpiresAt: s.now().Add(inviteTokenTTL),
		CreatedBy: actorID,
	}
	// 发送（未配置/失败不阻断创建）
	cfg, cfgErr := s.smtpConfig(ctx)
	switch {
	case cfgErr != nil && !errors.Is(cfgErr, mail.ErrNotConfigured):
		res.Error = cfgErr.Error()
		return res
	case cfg == nil || !cfg.Valid():
		m.SendStatus = model.InviteSentNotSent
		res.Link = s.inviteLink(token)
	default:
		if serr := s.sender.Send(ctx, email, "Woodpecker 知识库邀请", s.inviteBody(token)); serr != nil {
			m.SendStatus = model.InviteSentFailed
			m.SendError = ptr(serr.Error())
			res.Error = "邮件发送失败: " + serr.Error()
		} else {
			m.SendStatus = 1
			res.Sent = true
		}
		// 链接始终返回：邮件丢失时管理员可复制手动转发
		res.Link = s.inviteLink(token)
	}
	if err := s.invites.Upsert(ctx, m); err != nil {
		res.Error = err.Error()
		return res
	}
	saved, err := s.invites.GetByEmail(ctx, email)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	res.Invite = s.toItem(saved)
	res.Link = s.inviteLink(token)
	return res
}

// smtpConfig 读 SMTP 配置；未配置返回 mail.ErrNotConfigured 且 cfg 为 nil。
func (s *InviteService) smtpConfig(ctx context.Context) (*mail.Config, error) {
	if s.smtp == nil {
		return nil, mail.ErrNotConfigured
	}
	return s.smtp.Load(ctx)
}

func (s *InviteService) inviteLink(token string) string {
	if s.baseURL == "" {
		return ""
	}
	return s.baseURL + "/register?invite=" + url.QueryEscape(token)
}

// List 邀请列表。
func (s *InviteService) List(ctx context.Context, status string, page, size int) ([]InviteItem, int64, error) {
	q := repo.InviteListQuery{Page: page, Size: size}
	if status != "" {
		var st int16
		switch status {
		case "pending":
			st = model.InviteStatusPending
		case "accepted":
			st = model.InviteStatusAccepted
		case "revoked":
			st = model.InviteStatusRevoked
		default:
			return nil, 0, NewServiceError(CodeValidation, "status 取值 pending|accepted|revoked")
		}
		q.Status = &st
	}
	rows, total, err := s.invites.List(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	items := make([]InviteItem, 0, len(rows))
	for i := range rows {
		items = append(items, *s.toItem(&rows[i]))
	}
	return items, total, nil
}

// Resend 重发（覆盖 token、重置有效期与发送状态）。
func (s *InviteService) Resend(ctx context.Context, actorID, id int64, ip string) (*InviteItem, error) {
	m, err := s.invites.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "邀请不存在")
		}
		return nil, err
	}
	if m.Status == model.InviteStatusAccepted {
		return nil, NewServiceError(CodeInviteUsed, "该邀请已被使用")
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	m.TokenHash = mail.HashToken(token)
	m.Status = model.InviteStatusPending
	m.ExpiresAt = s.now().Add(inviteTokenTTL)
	cfg, cfgErr := s.smtpConfig(ctx)
	sendErr := ""
	switch {
	case cfgErr != nil && !errors.Is(cfgErr, mail.ErrNotConfigured):
		return nil, cfgErr
	case cfg == nil || !cfg.Valid():
		m.SendStatus = model.InviteSentNotSent
		m.SendError = nil
	default:
		if serr := s.sender.Send(ctx, m.Email, "Woodpecker 知识库邀请", s.inviteBody(token)); serr != nil {
			m.SendStatus = model.InviteSentFailed
			sendErr = serr.Error()
			m.SendError = ptr(sendErr)
		} else {
			m.SendStatus = 1
			m.SendError = nil
		}
	}
	m.UpdatedAt = s.now()
	if err := s.invites.Upsert(ctx, m); err != nil {
		return nil, err
	}
	if m.SendStatus == model.InviteSentFailed {
		_ = s.invites.MarkSent(ctx, m.ID, model.InviteSentFailed, sendErr)
	}
	s.audit.Record(ctx, &actorID, "invite.resend", "user", &m.ID,
		map[string]any{"email": m.Email}, ip)
	saved, err := s.invites.GetByID(ctx, m.ID)
	if err != nil {
		return nil, err
	}
	item := s.toItem(saved)
	item.Link = s.inviteLink(token)
	return item, nil
}

// Revoke 撤销未接受邀请。
func (s *InviteService) Revoke(ctx context.Context, actorID, id int64, ip string) error {
	m, err := s.invites.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "邀请不存在")
		}
		return err
	}
	if m.Status == model.InviteStatusAccepted {
		return NewServiceError(CodeInviteUsed, "该邀请已被使用，不可撤销")
	}
	if err := s.invites.Revoke(ctx, id); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "invite.revoke", "user", &m.ID,
		map[string]any{"email": m.Email}, ip)
	return nil
}

// Validate 公开端点：校验 token，返回可展示的邮箱与过期时间。
func (s *InviteService) Validate(ctx context.Context, token string) (email string, expiresAt time.Time, err error) {
	m, err := s.invites.GetByTokenHash(ctx, mail.HashToken(token))
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return "", time.Time{}, NewServiceError(CodeInviteInvalid, "邀请无效或已过期")
		}
		return "", time.Time{}, err
	}
	if m.Status == model.InviteStatusAccepted {
		return "", time.Time{}, NewServiceError(CodeInviteUsed, "该邀请已被使用")
	}
	if m.Status == model.InviteStatusRevoked || s.now().After(m.ExpiresAt) {
		return "", time.Time{}, NewServiceError(CodeInviteInvalid, "邀请无效或已过期")
	}
	return m.Email, m.ExpiresAt, nil
}

// randomToken 32 字节随机 → hex（64 字符）。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成邀请 token 失败: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// inviteBody 邀请邮件正文。
func (s *InviteService) inviteBody(token string) string {
	link := s.inviteLink(token)
	return strings.Join([]string{
		"您好，",
		"",
		"您被邀请加入 Woodpecker 企业知识库。请在 7 天内点击以下链接完成注册（设置用户名与密码）：",
		"",
		link,
		"",
		"如果链接无法点击，请复制到浏览器地址栏打开。此邀请为一次性链接，注册成功后即失效。",
		"",
		"Woodpecker 企业知识库",
	}, "\n")
}

func ptr[T any](v T) *T { return &v }
