package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/jwt"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// ---- 内存 fake 实现（单元测试用）----

type fakeUsers struct {
	mu     sync.Mutex
	byName map[string]*model.User
	nextID int64
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byName: map[string]*model.User{}, nextID: 1}
}

func (f *fakeUsers) Count(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.byName)), nil
}

func (f *fakeUsers) CountAdmins(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, u := range f.byName {
		if u.IsAdmin {
			n++
		}
	}
	return n, nil
}

func (f *fakeUsers) GetByUsername(_ context.Context, name string) (*model.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byName[name]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, repo.ErrNotFound
}

func (f *fakeUsers) GetByID(_ context.Context, id int64) (*model.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byName {
		if u.ID == id {
			cp := *u
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (f *fakeUsers) List(context.Context, repo.UserListQuery) ([]model.User, int64, error) {
	return nil, 0, errors.New("not implemented in fake")
}

func (f *fakeUsers) Create(_ context.Context, u *model.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byName[u.Username]; ok {
		return errors.New("duplicate")
	}
	u.ID = f.nextID
	f.nextID++
	f.byName[u.Username] = u
	return nil
}

func (f *fakeUsers) Save(_ context.Context, u *model.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byName[u.Username]; !ok {
		return repo.ErrNotFound
	}
	f.byName[u.Username] = u
	return nil
}

type fakeSettings struct{ vals map[string]bool }

func (f fakeSettings) GetBool(_ context.Context, key string, def bool) (bool, error) {
	if v, ok := f.vals[key]; ok {
		return v, nil
	}
	return def, nil
}

type fakeAudit struct{}

func (fakeAudit) Add(context.Context, *model.AuditLog) error { return nil }

func newTestAuth(t *testing.T, settings map[string]bool) (*AuthService, *fakeUsers, func() time.Time) {
	t.Helper()
	users := newFakeUsers()
	audit := NewAuditService(fakeAudit{})
	issuer := jwt.NewIssuer("test-secret", time.Hour)
	svc := NewAuthService(users, fakeSettings{vals: settings}, audit, issuer)
	base := time.Now()
	now := func() time.Time { return base }
	svc.now = now
	return svc, users, now
}

func mustRegister(t *testing.T, svc *AuthService, name, pw string) *AuthResult {
	t.Helper()
	res, err := svc.Register(context.Background(), name, pw, "127.0.0.1")
	require.NoError(t, err)
	return res
}

func svcErr(t *testing.T, err error) *ServiceError {
	t.Helper()
	require.Error(t, err)
	se, ok := err.(*ServiceError)
	require.True(t, ok, "期望 ServiceError，实际 %T", err)
	return se
}

// ---- 注册（FR-01）----

func TestRegisterBootstrapFirstAdmin(t *testing.T) {
	svc, users, _ := newTestAuth(t, nil) // 开关默认关闭
	res, err := svc.Register(context.Background(), "root", "password8", "ip")
	require.NoError(t, err)
	assert.True(t, res.IsAdmin, "首个注册用户自动成为管理员")
	assert.False(t, res.MustChangePassword)
	assert.True(t, users.byName["root"].IsAdmin)

	// 此后开关关闭 → AUTH_005
	_, err = svc.Register(context.Background(), "second", "password8", "ip")
	assert.Equal(t, CodeRegisterClosed, svcErr(t, err).Code)

	// 开关打开 → 放行（先种一个 bootstrap 用户，再注册的应为普通用户）
	svc2, users2, _ := newTestAuth(t, map[string]bool{"self_register_enabled": true})
	mustRegister(t, svc2, "seed", "password8")
	_, err = svc2.Register(context.Background(), "open-user", "password8", "ip")
	require.NoError(t, err)
	assert.False(t, users2.byName["open-user"].IsAdmin)
}

func TestRegisterDuplicateAndWeak(t *testing.T) {
	svc, _, _ := newTestAuth(t, map[string]bool{"self_register_enabled": true})
	mustRegister(t, svc, "alice", "password8")

	_, err := svc.Register(context.Background(), "alice", "password8", "ip")
	assert.Equal(t, CodeUserExists, svcErr(t, err).Code)

	_, err = svc.Register(context.Background(), "bob", "short", "ip")
	assert.Equal(t, CodePasswordWeak, svcErr(t, err).Code)
}

// ---- 登录与锁定（FR-01）----

func TestLoginAndLockout(t *testing.T) {
	svc, users, _ := newTestAuth(t, nil)
	mustRegister(t, svc, "alice", "password8")

	// AUTH_001：密码错误
	_, err := svc.Login(context.Background(), "alice", "wrong-pass", "ip")
	assert.Equal(t, CodeAuthInvalid, svcErr(t, err).Code)

	// 连续错 5 次 → 锁定
	for i := 0; i < 4; i++ {
		_, _ = svc.Login(context.Background(), "alice", "wrong-pass", "ip")
	}
	_, err = svc.Login(context.Background(), "alice", "wrong-pass", "ip")
	assert.Equal(t, CodeAuthLocked, svcErr(t, err).Code, "第 5 次失败即锁定")
	u := users.byName["alice"]
	require.NotNil(t, u.LockedUntil)

	// 锁定期间密码正确也拒绝
	_, err = svc.Login(context.Background(), "alice", "password8", "ip")
	assert.Equal(t, CodeAuthLocked, svcErr(t, err).Code)

	// 锁过期后可登录并复位计数
	users.mu.Lock()
	expired := time.Now().Add(-time.Minute)
	users.byName["alice"].LockedUntil = &expired
	users.byName["alice"].FailedAttempts = 5
	users.mu.Unlock()
	res, err := svc.Login(context.Background(), "alice", "password8", "ip")
	require.NoError(t, err)
	assert.Equal(t, "alice", res.Username)
	assert.Zero(t, users.byName["alice"].FailedAttempts)
	assert.Nil(t, users.byName["alice"].LockedUntil)
}

func TestLoginUnknownUserAndDisabled(t *testing.T) {
	svc, users, _ := newTestAuth(t, nil)
	mustRegister(t, svc, "alice", "password8")

	_, err := svc.Login(context.Background(), "ghost", "password8", "ip")
	assert.Equal(t, CodeAuthInvalid, svcErr(t, err).Code)

	users.mu.Lock()
	users.byName["alice"].Status = model.UserStatusDisabled
	users.mu.Unlock()
	_, err = svc.Login(context.Background(), "alice", "password8", "ip")
	assert.Equal(t, CodeAuthDisabled, svcErr(t, err).Code)
}

// ---- 改密（FR-01：首登强制改密 + token_version 失效）----

func TestChangePasswordInvalidatesOldToken(t *testing.T) {
	svc, users, _ := newTestAuth(t, nil)
	res := mustRegister(t, svc, "alice", "password8")
	oldVersion := users.byName["alice"].TokenVersion

	newRes, err := svc.ChangePassword(context.Background(), res.UserID, "password8", "newpass99")
	require.NoError(t, err)
	assert.False(t, newRes.MustChangePassword)
	assert.Equal(t, oldVersion+1, users.byName["alice"].TokenVersion)

	// 旧 token（旧版本号）应失效：issuer.Parse 本身通过，但中间件比对版本会拒
	oldClaims, err := svc.issuer.Parse(res.Token)
	require.NoError(t, err)
	assert.NotEqual(t, users.byName["alice"].TokenVersion, oldClaims.TokenVersion)

	// 原密码错误 / 新密码过短
	_, err = svc.ChangePassword(context.Background(), res.UserID, "bad-old", "newpass99")
	assert.Equal(t, CodeAuthInvalid, svcErr(t, err).Code)
	_, err = svc.ChangePassword(context.Background(), res.UserID, "newpass99", "short")
	assert.Equal(t, CodePasswordWeak, svcErr(t, err).Code)
}

// ---- 管理员用户管理（FR-13：USER_002/003 保护）----

func TestAdminCreateResetDisable(t *testing.T) {
	svc, users, _ := newTestAuth(t, nil)
	admin := mustRegister(t, svc, "root", "password8")
	adminSvc := NewUserAdminService(svc.users, svc.audit)
	adminSvc.now = svc.now

	// 创建用户：初始密码 + 强制改密
	initial, u, err := adminSvc.Create(context.Background(), admin.UserID, "newbie", "", false, "ip")
	require.NoError(t, err)
	assert.Len(t, initial, 12)
	assert.True(t, u.MustChangePassword)
	_, _, err = adminSvc.Create(context.Background(), admin.UserID, "newbie", "", false, "ip")
	assert.Equal(t, CodeUserExists, svcErr(t, err).Code, "重名 USER_001")

	// 重置密码：踢出会话
	_, _, err = adminSvc.Create(context.Background(), admin.UserID, "staff", "password8", false, "ip")
	require.NoError(t, err)
	staffVer := users.byName["staff"].TokenVersion
	newPw, err := adminSvc.ResetPassword(context.Background(), admin.UserID, users.byName["staff"].ID, "ip")
	require.NoError(t, err)
	assert.Len(t, newPw, 12)
	assert.Equal(t, staffVer+1, users.byName["staff"].TokenVersion)
	assert.True(t, users.byName["staff"].MustChangePassword)

	// USER_002：不能禁用自己
	err = adminSvc.SetStatus(context.Background(), admin.UserID, admin.UserID, true, "ip")
	assert.Equal(t, CodeUserSelfDisable, svcErr(t, err).Code)

	// 正常禁用他人
	require.NoError(t, adminSvc.SetStatus(context.Background(), admin.UserID, users.byName["newbie"].ID, true, "ip"))
	assert.Equal(t, model.UserStatusDisabled, users.byName["newbie"].Status)
	// 重新启用
	require.NoError(t, adminSvc.SetStatus(context.Background(), admin.UserID, users.byName["newbie"].ID, false, "ip"))
	assert.Equal(t, model.UserStatusActive, users.byName["newbie"].Status)
}

func TestAdminDisableLastAdmin(t *testing.T) {
	// 两个管理员，先降级一个，再用另一个管理员禁/降最后一个 → USER_003
	svc, users, _ := newTestAuth(t, nil)
	root := mustRegister(t, svc, "root", "password8")
	rootSvc := NewUserAdminService(svc.users, svc.audit)
	rootSvc.now = svc.now

	_, _, _ = rootSvc.Create(context.Background(), root.UserID, "root2", "password8", true, "ip")
	require.NoError(t, rootSvc.SetAdmin(context.Background(), root.UserID, users.byName["root2"].ID, false, "ip"))

	// 以 root2 的身份操作（service 层不做操作者权限校验，校验在中间件）
	err := rootSvc.SetStatus(context.Background(), users.byName["root2"].ID, root.UserID, true, "ip")
	assert.Equal(t, CodeUserLastAdmin, svcErr(t, err).Code, "仅剩一个管理员时不可禁用")
	err = rootSvc.SetAdmin(context.Background(), users.byName["root2"].ID, root.UserID, false, "ip")
	assert.Equal(t, CodeUserLastAdmin, svcErr(t, err).Code)
	assert.True(t, users.byName["root"].IsAdmin)
}

func TestAdminSetAdminLastAdminProtection(t *testing.T) {
	svc, users, _ := newTestAuth(t, nil)
	root := mustRegister(t, svc, "root", "password8")
	adminSvc := NewUserAdminService(svc.users, svc.audit)
	adminSvc.now = svc.now

	err := adminSvc.SetAdmin(context.Background(), root.UserID, root.UserID, false, "ip")
	assert.Equal(t, CodeUserLastAdmin, svcErr(t, err).Code, "唯一管理员不可自降级")
	assert.True(t, users.byName["root"].IsAdmin)
}
