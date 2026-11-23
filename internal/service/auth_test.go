package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

func svcErr(t *testing.T, err error) *service.ServiceError {
	t.Helper()
	require.Error(t, err)
	var se *service.ServiceError
	require.ErrorAs(t, err, &se, "期望 ServiceError，实际 %T", err)
	return se
}

// ---- 注册（FR-01）----

func TestRegisterBootstrapFirstAdmin(t *testing.T) {
	env := testutil.NewTestEnv(t, nil) // 开关默认关闭
	res, err := env.AuthSvc.Register(context.Background(), "root", "password8", "ip")
	require.NoError(t, err)
	assert.True(t, res.IsAdmin, "首个注册用户自动成为管理员")
	assert.False(t, res.MustChangePassword)
	assert.True(t, env.Users.Snapshot("root").IsAdmin)
	assert.NotEmpty(t, res.Token)

	// 此后开关关闭 → AUTH_005
	_, err = env.AuthSvc.Register(context.Background(), "second", "password8", "ip")
	assert.Equal(t, service.CodeRegisterClosed, svcErr(t, err).Code)

	// 开关打开 → 放行（先种 bootstrap 用户，再注册的应为普通用户）
	env2 := testutil.NewTestEnv(t, map[string]bool{"self_register_enabled": true})
	_, err = env2.AuthSvc.Register(context.Background(), "seed", "password8", "ip")
	require.NoError(t, err)
	res2, err := env2.AuthSvc.Register(context.Background(), "open-user", "password8", "ip")
	require.NoError(t, err)
	assert.False(t, res2.IsAdmin)
	assert.False(t, env2.Users.Snapshot("open-user").IsAdmin)
}

func TestRegisterDuplicateAndWeak(t *testing.T) {
	env := testutil.NewTestEnv(t, map[string]bool{"self_register_enabled": true})
	_, err := env.AuthSvc.Register(context.Background(), "alice", "password8", "ip")
	require.NoError(t, err)

	_, err = env.AuthSvc.Register(context.Background(), "alice", "password8", "ip")
	assert.Equal(t, service.CodeUserExists, svcErr(t, err).Code)

	_, err = env.AuthSvc.Register(context.Background(), "bob", "short", "ip")
	assert.Equal(t, service.CodePasswordWeak, svcErr(t, err).Code)

	_, err = env.AuthSvc.Register(context.Background(), "", "password8", "ip")
	assert.Equal(t, service.CodeAuthInvalid, svcErr(t, err).Code, "空用户名")
}

func TestRegisterSettingsError(t *testing.T) {
	env := testutil.NewTestEnv(t, nil)
	env.Settings.Err = errors.New("db down")
	// 已有一个用户，走开关路径，GetBool 出错 → 透传
	_, err := env.AuthSvc.Register(context.Background(), "seed", "password8", "ip")
	require.NoError(t, err)
	_, err = env.AuthSvc.Register(context.Background(), "other", "password8", "ip")
	require.Error(t, err)
	assert.Nil(t, svcErrNil(t, err), "非业务错误应透传")
}

func svcErrNil(t *testing.T, err error) *service.ServiceError {
	t.Helper()
	var se *service.ServiceError
	_ = errors.As(err, &se)
	return se
}

// ---- 登录与锁定（FR-01）----

func TestLoginAndLockout(t *testing.T) {
	env := testutil.NewTestEnv(t, nil)
	_, err := env.AuthSvc.Register(context.Background(), "alice", "password8", "ip")
	require.NoError(t, err)

	// AUTH_001：密码错误
	_, err = env.AuthSvc.Login(context.Background(), "alice", "wrong-pass", "ip")
	assert.Equal(t, service.CodeAuthInvalid, svcErr(t, err).Code)

	// 连续错 5 次 → 锁定
	for i := 0; i < 4; i++ {
		_, _ = env.AuthSvc.Login(context.Background(), "alice", "wrong-pass", "ip")
	}
	_, err = env.AuthSvc.Login(context.Background(), "alice", "wrong-pass", "ip")
	assert.Equal(t, service.CodeAuthLocked, svcErr(t, err).Code, "第 5 次失败即锁定")
	locked := env.Users.Snapshot("alice")
	require.NotNil(t, locked.LockedUntil)

	// 锁定期间密码正确也拒绝
	_, err = env.AuthSvc.Login(context.Background(), "alice", "password8", "ip")
	assert.Equal(t, service.CodeAuthLocked, svcErr(t, err).Code)

	// 锁过期后可登录并复位计数
	expired := time.Now().Add(-time.Minute)
	env.Users.Mutate("alice", func(u *model.User) { u.LockedUntil = &expired; u.FailedAttempts = 5 })
	res, err := env.AuthSvc.Login(context.Background(), "alice", "password8", "ip")
	require.NoError(t, err)
	assert.Equal(t, "alice", res.Username)
	assert.Zero(t, env.Users.Snapshot("alice").FailedAttempts)
	assert.Nil(t, env.Users.Snapshot("alice").LockedUntil)
}

func TestLoginUnknownUserAndDisabled(t *testing.T) {
	env := testutil.NewTestEnv(t, nil)
	_, err := env.AuthSvc.Register(context.Background(), "alice", "password8", "ip")
	require.NoError(t, err)

	_, err = env.AuthSvc.Login(context.Background(), "ghost", "password8", "ip")
	assert.Equal(t, service.CodeAuthInvalid, svcErr(t, err).Code)

	env.Users.Mutate("alice", func(u *model.User) { u.Status = model.UserStatusDisabled })
	_, err = env.AuthSvc.Login(context.Background(), "alice", "password8", "ip")
	assert.Equal(t, service.CodeAuthDisabled, svcErr(t, err).Code)
}

// ---- 改密（FR-01：首登强制改密 + token_version 失效 + 审计）----

func TestChangePasswordInvalidatesOldToken(t *testing.T) {
	env := testutil.NewTestEnv(t, nil)
	res, err := env.AuthSvc.Register(context.Background(), "alice", "password8", "ip")
	require.NoError(t, err)
	oldVersion := env.Users.Snapshot("alice").TokenVersion

	newRes, err := env.AuthSvc.ChangePassword(context.Background(), res.UserID, "password8", "newpass99")
	require.NoError(t, err)
	assert.False(t, newRes.MustChangePassword)
	assert.Equal(t, oldVersion+1, env.Users.Snapshot("alice").TokenVersion)

	// 旧 token 携带旧版本号 → 中间件比对会拒绝
	oldClaims, err := env.Issuer.Parse(res.Token)
	require.NoError(t, err)
	assert.NotEqual(t, env.Users.Snapshot("alice").TokenVersion, oldClaims.TokenVersion)

	// 原密码错误 / 新密码过短
	_, err = env.AuthSvc.ChangePassword(context.Background(), res.UserID, "bad-old", "newpass99")
	assert.Equal(t, service.CodeAuthInvalid, svcErr(t, err).Code)
	_, err = env.AuthSvc.ChangePassword(context.Background(), res.UserID, "newpass99", "short")
	assert.Equal(t, service.CodePasswordWeak, svcErr(t, err).Code)

	// 审计：user.change_password
	assert.Equal(t, 1, env.Audit.CountAction("user.change_password"))
}

// ---- 审计失败不影响主流程（FR-15 例外）----

func TestAuditFailureDoesNotBlockLogin(t *testing.T) {
	env := testutil.NewTestEnv(t, nil)
	env.Audit.FailNext = true
	_, err := env.AuthSvc.Register(context.Background(), "alice", "password8", "ip")
	require.NoError(t, err, "审计写入失败不应阻塞注册")
}

// ---- 管理员用户管理（FR-13）----

func TestAdminCreateListResetDisable(t *testing.T) {
	env := testutil.NewTestEnv(t, nil)
	admin := env.SeedUser(t, "root", "password8", true)

	// 创建用户：初始密码 + 强制改密
	initial, u, err := env.AdminSvc.Create(context.Background(), admin.ID, "newbie", "", false, "ip")
	require.NoError(t, err)
	assert.Len(t, initial, 12)
	assert.True(t, u.MustChangePassword)
	_, _, err = env.AdminSvc.Create(context.Background(), admin.ID, "newbie", "", false, "ip")
	assert.Equal(t, service.CodeUserExists, svcErr(t, err).Code, "重名 USER_001")

	// 弱密码（指定密码过短）
	_, _, err = env.AdminSvc.Create(context.Background(), admin.ID, "weak", "short", false, "ip")
	assert.Equal(t, service.CodePasswordWeak, svcErr(t, err).Code)

	// 列表（含 kb_count、关键词过滤）
	items, total, err := env.AdminSvc.List(context.Background(), repo.UserListQuery{Page: 1, PerPage: 20})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	assert.Len(t, items, 2)
	items, _, err = env.AdminSvc.List(context.Background(), repo.UserListQuery{Keyword: "newbie", Page: 1, PerPage: 20})
	require.NoError(t, err)
	assert.Len(t, items, 1)
	_, _, err = env.AdminSvc.List(context.Background(), repo.UserListQuery{Page: 1, PerPage: 20, Keyword: "x"})
	_ = err
	env.Users.ErrOnList = errors.New("db error")
	_, _, err = env.AdminSvc.List(context.Background(), repo.UserListQuery{Page: 1, PerPage: 20})
	require.Error(t, err)
	env.Users.ErrOnList = nil

	// 重置密码：踢出会话
	staffVer := env.Users.Snapshot("newbie").TokenVersion
	newPw, err := env.AdminSvc.ResetPassword(context.Background(), admin.ID, u.ID, "ip")
	require.NoError(t, err)
	assert.Len(t, newPw, 12)
	assert.Equal(t, staffVer+1, env.Users.Snapshot("newbie").TokenVersion)
	assert.True(t, env.Users.Snapshot("newbie").MustChangePassword)

	// USER_002：不能禁用自己
	err = env.AdminSvc.SetStatus(context.Background(), admin.ID, admin.ID, true, "ip")
	assert.Equal(t, service.CodeUserSelfDisable, svcErr(t, err).Code)

	// 目标用户不存在 → NOT_FOUND(404)
	err = env.AdminSvc.SetStatus(context.Background(), admin.ID, 999, true, "ip")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)

	// 正常禁用/启用
	require.NoError(t, env.AdminSvc.SetStatus(context.Background(), admin.ID, u.ID, true, "ip"))
	assert.Equal(t, model.UserStatusDisabled, env.Users.Snapshot("newbie").Status)
	require.NoError(t, env.AdminSvc.SetStatus(context.Background(), admin.ID, u.ID, false, "ip"))
	assert.Equal(t, model.UserStatusActive, env.Users.Snapshot("newbie").Status)
}

func TestAdminSetAdminLastAdminProtection(t *testing.T) {
	env := testutil.NewTestEnv(t, nil)
	root := env.SeedUser(t, "root", "password8", true)
	_, _, _ = env.AdminSvc.Create(context.Background(), root.ID, "root2", "password8", true, "ip")
	require.NoError(t, env.AdminSvc.SetAdmin(context.Background(), root.ID, env.Users.Snapshot("root2").ID, false, "ip"))

	err := env.AdminSvc.SetStatus(context.Background(), env.Users.Snapshot("root2").ID, root.ID, true, "ip")
	assert.Equal(t, service.CodeUserLastAdmin, svcErr(t, err).Code, "仅剩一个管理员时不可禁用")
	err = env.AdminSvc.SetAdmin(context.Background(), env.Users.Snapshot("root2").ID, root.ID, false, "ip")
	assert.Equal(t, service.CodeUserLastAdmin, svcErr(t, err).Code)
	assert.True(t, env.Users.Snapshot("root").IsAdmin)
}
