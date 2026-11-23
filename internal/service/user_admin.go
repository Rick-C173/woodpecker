package service

import (
	"context"
	"errors"
	"time"

	"woodpecker/internal/crypto"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// UserAdminService 管理员用户管理（PRD FR-13），含 USER_002/003 保护。
type UserAdminService struct {
	users  UserRepo
	audit  *AuditService
	policy PasswordPolicy
	now    func() time.Time
}

func NewUserAdminService(users UserRepo, audit *AuditService) *UserAdminService {
	return &UserAdminService{users: users, audit: audit, policy: DefaultPasswordPolicy(), now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *UserAdminService) SetClock(now func() time.Time) { s.now = now }

// SetPolicy 注入密码策略（默认 DefaultPasswordPolicy）。
func (s *UserAdminService) SetPolicy(p PasswordPolicy) { s.policy = p }

type AdminUserItem struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	IsAdmin            bool   `json:"is_admin"`
	Status             int16  `json:"status"`
	MustChangePassword bool   `json:"must_change_password"`
	KBCount            int64  `json:"kb_count"`
	CreatedAt          string `json:"created_at"`
}

func (s *UserAdminService) toItem(ctx context.Context, u *model.User) (*AdminUserItem, error) {
	var kbCount int64
	if counter, ok := s.users.(interface {
		CountKBMemberships(ctx context.Context, userID int64) (int64, error)
	}); ok {
		var err error
		kbCount, err = counter.CountKBMemberships(ctx, u.ID)
		if err != nil {
			return nil, err
		}
	}
	return &AdminUserItem{
		ID:                 u.ID,
		Username:           u.Username,
		IsAdmin:            u.IsAdmin,
		Status:             u.Status,
		MustChangePassword: u.MustChangePassword,
		KBCount:            kbCount,
		CreatedAt:          u.CreatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}

// List 用户列表（FR-13）。
func (s *UserAdminService) List(ctx context.Context, q repo.UserListQuery) ([]AdminUserItem, int64, error) {
	users, total, err := s.users.List(ctx, q)
	if err != nil {
		return nil, 0, err
	}
	items := make([]AdminUserItem, 0, len(users))
	for i := range users {
		item, err := s.toItem(ctx, &users[i])
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	return items, total, nil
}

// Create 创建用户（FR-13）：初始密码随机生成或指定，首次登录强制修改。
// 返回一次性展示的初始密码。
func (s *UserAdminService) Create(ctx context.Context, actorID int64, username, password string, isAdmin bool, ip string) (string, *model.User, error) {
	if password == "" {
		gen, err := crypto.RandomPassword(s.policy.InitialLen)
		if err != nil {
			return "", nil, err
		}
		password = gen
	}
	if err := validateCredentials(username, password, s.policy.MinLen); err != nil {
		return "", nil, err
	}
	if _, err := s.users.GetByUsername(ctx, username); err == nil {
		return "", nil, NewServiceError(CodeUserExists, "用户名已存在")
	} else if !errors.Is(err, repo.ErrNotFound) {
		return "", nil, err
	}
	hash, err := crypto.HashPassword(password)
	if err != nil {
		return "", nil, err
	}
	u := &model.User{
		Username:           username,
		PasswordHash:       hash,
		IsAdmin:            isAdmin,
		Status:             model.UserStatusActive,
		MustChangePassword: true,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return "", nil, err
	}
	s.audit.Record(ctx, &actorID, "user.create", "user", &u.ID,
		map[string]any{"username": username, "is_admin": isAdmin}, ip)
	return password, u, nil
}

// SetStatus 禁用/启用（FR-13）：禁用立即踢出（token_version+1），数据保留。
func (s *UserAdminService) SetStatus(ctx context.Context, actorID, targetID int64, disable bool, ip string) error {
	target, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return NewServiceError(CodeNotFound, "用户不存在")
	}
	if disable {
		if targetID == actorID {
			return NewServiceError(CodeUserSelfDisable, "不能禁用自己")
		}
		if target.IsAdmin {
			admins, err := s.users.CountAdmins(ctx)
			if err != nil {
				return err
			}
			if admins <= 1 {
				return NewServiceError(CodeUserLastAdmin, "最后一个管理员不可被禁用")
			}
		}
		target.Status = model.UserStatusDisabled
		target.TokenVersion++ // 立即失效其全部 token
		target.UpdatedAt = s.now()
		if err := s.users.Save(ctx, target); err != nil {
			return err
		}
		s.audit.Record(ctx, &actorID, "user.disable", "user", &targetID,
			map[string]any{"username": target.Username}, ip)
		return nil
	}
	target.Status = model.UserStatusActive
	target.FailedAttempts = 0
	target.LockedUntil = nil
	target.UpdatedAt = s.now()
	if err := s.users.Save(ctx, target); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "user.enable", "user", &targetID,
		map[string]any{"username": target.Username}, ip)
	return nil
}

// SetAdmin 设/撤管理员（FR-13）：最后一个管理员不可被降级。
func (s *UserAdminService) SetAdmin(ctx context.Context, actorID, targetID int64, makeAdmin bool, ip string) error {
	target, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return NewServiceError(CodeNotFound, "用户不存在")
	}
	if !makeAdmin && target.IsAdmin {
		admins, err := s.users.CountAdmins(ctx)
		if err != nil {
			return err
		}
		if admins <= 1 {
			return NewServiceError(CodeUserLastAdmin, "最后一个管理员不可被降级")
		}
	}
	target.IsAdmin = makeAdmin
	target.UpdatedAt = s.now()
	if err := s.users.Save(ctx, target); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "user.set_admin", "user", &targetID,
		map[string]any{"username": target.Username, "is_admin": makeAdmin}, ip)
	return nil
}

// ResetPassword 重置密码（FR-13）：生成新初始密码，下次登录强制修改，并踢出全部会话。
func (s *UserAdminService) ResetPassword(ctx context.Context, actorID, targetID int64, ip string) (string, error) {
	target, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return "", NewServiceError(CodeNotFound, "用户不存在")
	}
	newPw, err := crypto.RandomPassword(s.policy.InitialLen)
	if err != nil {
		return "", err
	}
	hash, err := crypto.HashPassword(newPw)
	if err != nil {
		return "", err
	}
	target.PasswordHash = hash
	target.MustChangePassword = true
	target.TokenVersion++
	target.FailedAttempts = 0
	target.LockedUntil = nil
	target.UpdatedAt = s.now()
	if err := s.users.Save(ctx, target); err != nil {
		return "", err
	}
	s.audit.Record(ctx, &actorID, "user.reset_password", "user", &targetID,
		map[string]any{"username": target.Username}, ip)
	return newPw, nil
}
