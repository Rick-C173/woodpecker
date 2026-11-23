package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"woodpecker/internal/crypto"
	"woodpecker/internal/jwt"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// 业务错误：Code 与 md/API设计.md §2 一一对应，handler 层据此渲染。
type ServiceError struct {
	Code    string
	Message string
}

func (e *ServiceError) Error() string { return e.Code + ": " + e.Message }

func NewServiceError(code, message string) *ServiceError {
	return &ServiceError{Code: code, Message: message}
}

// 错误码常量（与 internal/httpx/errcode.go 保持同步）
const (
	CodeAuthInvalid     = "AUTH_001"
	CodeAuthLocked      = "AUTH_002"
	CodeAuthDisabled    = "AUTH_003"
	CodeAuthStaleToken  = "AUTH_004"
	CodeRegisterClosed  = "AUTH_005"
	CodePasswordWeak    = "AUTH_006"
	CodeUserExists      = "USER_001"
	CodeUserSelfDisable = "USER_002"
	CodeUserLastAdmin   = "USER_003"
	CodeKBNameExists    = "KB_001"
	CodeNotFound        = "NOT_FOUND"
	CodeValidation      = "VALIDATION_FAILED"
	CodeForbidden       = "FORBIDDEN"
)

// PasswordPolicy 密码与锁定策略（PRD FR-01）：默认值见 DefaultPasswordPolicy，
// 可通过 SetPolicy 由配置文件注入。
type PasswordPolicy struct {
	MinLen        int           // 密码最小长度
	LockThreshold int           // 连续失败 N 次锁定
	LockDuration  time.Duration // 锁定时长
	InitialLen    int           // 随机初始密码长度
}

func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{MinLen: 8, LockThreshold: 5, LockDuration: 10 * time.Minute, InitialLen: 12}
}

type UserRepo interface {
	Count(ctx context.Context) (int64, error)
	CountAdmins(ctx context.Context) (int64, error)
	GetByUsername(ctx context.Context, username string) (*model.User, error)
	GetByID(ctx context.Context, id int64) (*model.User, error)
	List(ctx context.Context, q repo.UserListQuery) ([]model.User, int64, error)
	Create(ctx context.Context, u *model.User) error
	Save(ctx context.Context, u *model.User) error
}

type AuthService struct {
	users    UserRepo
	settings SettingRepo
	audit    *AuditService
	issuer   *jwt.Issuer
	policy   PasswordPolicy
	now      func() time.Time
}

func NewAuthService(users UserRepo, settings SettingRepo, audit *AuditService, issuer *jwt.Issuer) *AuthService {
	return &AuthService{users: users, settings: settings, audit: audit, issuer: issuer, policy: DefaultPasswordPolicy(), now: time.Now}
}

// SetClock 注入时钟（测试用）。
func (s *AuthService) SetClock(now func() time.Time) { s.now = now }

// SetPolicy 注入密码策略（默认 DefaultPasswordPolicy）。
func (s *AuthService) SetPolicy(p PasswordPolicy) { s.policy = p }

type AuthResult struct {
	Token              string `json:"token"`
	MustChangePassword bool   `json:"must_change_password"`
	IsAdmin            bool   `json:"is_admin"`
	UserID             int64  `json:"user_id"`
	Username           string `json:"username"`
}

func (s *AuthService) result(u *model.User) (*AuthResult, error) {
	token, err := s.issuer.Issue(u)
	if err != nil {
		return nil, err
	}
	return &AuthResult{
		Token:              token,
		MustChangePassword: u.MustChangePassword,
		IsAdmin:            u.IsAdmin,
		UserID:             u.ID,
		Username:           u.Username,
	}, nil
}

func (s *AuthService) Login(ctx context.Context, username, password, ip string) (*AuthResult, error) {
	u, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		s.audit.Record(ctx, nil, "auth.login_failed", "user", nil,
			map[string]any{"username": username, "reason": "unknown_user"}, ip)
		return nil, NewServiceError(CodeAuthInvalid, "用户名或密码错误")
	}
	if u.Status == model.UserStatusDisabled {
		return nil, NewServiceError(CodeAuthDisabled, "账号已被禁用，请联系管理员")
	}
	if u.LockedUntil != nil && u.LockedUntil.After(s.now()) {
		remaining := int(time.Until(*u.LockedUntil).Minutes()) + 1
		return nil, NewServiceError(CodeAuthLocked, fmt.Sprintf("账号已锁定，请 %d 分钟后重试", remaining))
	}

	ok, verr := crypto.VerifyPassword(password, u.PasswordHash)
	if verr != nil || !ok {
		u.FailedAttempts++
		detail := map[string]any{"username": username}
		if u.FailedAttempts >= s.policy.LockThreshold {
			locked := s.now().Add(s.policy.LockDuration)
			u.LockedUntil = &locked
			s.audit.Record(ctx, &u.ID, "auth.locked", "user", &u.ID, detail, ip)
		}
		u.UpdatedAt = s.now()
		if err := s.users.Save(ctx, u); err != nil {
			return nil, err
		}
		s.audit.Record(ctx, &u.ID, "auth.login_failed", "user", &u.ID, detail, ip)
		return nil, NewServiceError(CodeAuthInvalid, "用户名或密码错误")
	}

	u.FailedAttempts = 0
	u.LockedUntil = nil
	u.UpdatedAt = s.now()
	if err := s.users.Save(ctx, u); err != nil {
		return nil, err
	}
	return s.result(u)
}

// Register 自助注册（PRD FR-01）：仅当系统无任何用户（首管理员引导）或管理员开启开关时放行。
func (s *AuthService) Register(ctx context.Context, username, password, ip string) (*AuthResult, error) {
	total, err := s.users.Count(ctx)
	if err != nil {
		return nil, err
	}
	isBootstrap := total == 0
	if !isBootstrap {
		enabled, err := s.settings.GetBool(ctx, "self_register_enabled", false)
		if err != nil {
			return nil, err
		}
		if !enabled {
			return nil, NewServiceError(CodeRegisterClosed, "自助注册未开放，请联系管理员创建账号")
		}
	}
	if err := validateCredentials(username, password, s.policy.MinLen); err != nil {
		return nil, err
	}
	if _, err := s.users.GetByUsername(ctx, username); err == nil {
		return nil, NewServiceError(CodeUserExists, "用户名已存在")
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}

	hash, err := crypto.HashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &model.User{
		Username:     username,
		PasswordHash: hash,
		IsAdmin:      isBootstrap, // 第一个注册用户自动成为管理员
		Status:       model.UserStatusActive,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return s.result(u)
}

// ChangePassword 修改自己密码（含首登强制改密）：成功后 token_version+1，旧 token 全部失效。
func (s *AuthService) ChangePassword(ctx context.Context, userID int64, oldPassword, newPassword string) (*AuthResult, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, NewServiceError(CodeAuthInvalid, "用户不存在")
	}
	ok, verr := crypto.VerifyPassword(oldPassword, u.PasswordHash)
	if verr != nil || !ok {
		return nil, NewServiceError(CodeAuthInvalid, "原密码错误")
	}
	if len(newPassword) < s.policy.MinLen {
		return nil, NewServiceError(CodePasswordWeak, fmt.Sprintf("新密码长度不足 %d 位", s.policy.MinLen))
	}
	hash, err := crypto.HashPassword(newPassword)
	if err != nil {
		return nil, err
	}
	u.PasswordHash = hash
	u.MustChangePassword = false
	u.TokenVersion++
	u.UpdatedAt = s.now()
	if err := s.users.Save(ctx, u); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &u.ID, "user.change_password", "user", &u.ID, nil, "")
	return s.result(u)
}

func validateCredentials(username, password string, minLen int) error {
	if username == "" || len(username) > 64 {
		return NewServiceError(CodeAuthInvalid, "用户名非法（1~64 字符）")
	}
	if len(password) < minLen {
		return NewServiceError(CodePasswordWeak, fmt.Sprintf("密码长度不足 %d 位", minLen))
	}
	return nil
}
