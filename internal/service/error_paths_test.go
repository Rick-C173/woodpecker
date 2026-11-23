package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/repo"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

// 存储层错误注入：验证各分支的错误传播路径（语句覆盖）。

func TestServiceErrorString(t *testing.T) {
	e := service.NewServiceError("X_001", "msg")
	assert.Equal(t, "X_001: msg", e.Error())
}

func TestRepoErrorPropagation(t *testing.T) {
	boom := errors.New("db boom")

	t.Run("Login Save 失败（密码错误路径）", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		env.SeedUser(t, "alice", "password8", false)
		env.Users.ErrOnSave = boom
		_, err := env.AuthSvc.Login(context.Background(), "alice", "wrong-pass", "ip")
		require.Error(t, err)
		assert.Nil(t, svcErrNil(t, err), "存储错误应原样透传")
	})

	t.Run("Login Save 失败（成功路径）", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		env.SeedUser(t, "alice", "password8", false)
		env.Users.ErrOnSave = boom
		_, err := env.AuthSvc.Login(context.Background(), "alice", "password8", "ip")
		require.Error(t, err)
	})

	t.Run("Register Count 失败", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		env.Users.ErrOnCount = boom
		_, err := env.AuthSvc.Register(context.Background(), "a", "password8", "ip")
		require.Error(t, err)
	})

	t.Run("Register Create 失败", func(t *testing.T) {
		env := testutil.NewTestEnv(t, map[string]bool{"self_register_enabled": true})
		env.SeedUser(t, "seed", "password8", false)
		env.Users.ErrOnCreate = boom
		_, err := env.AuthSvc.Register(context.Background(), "other", "password8", "ip")
		require.Error(t, err)
	})

	t.Run("ChangePassword Save 失败", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		u := env.SeedUser(t, "alice", "password8", false)
		env.Users.ErrOnSave = boom
		_, err := env.AuthSvc.ChangePassword(context.Background(), u.ID, "password8", "newpass99")
		require.Error(t, err)
	})

	t.Run("AdminCreate 哈希之外的存储错误", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		admin := env.SeedUser(t, "root", "password8", true)
		env.Users.ErrOnCreate = boom
		_, _, err := env.AdminSvc.Create(context.Background(), admin.ID, "n1", "password8", false, "ip")
		require.Error(t, err)
	})

	t.Run("AdminList 错误已覆盖", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		env.Users.ErrOnList = boom
		_, _, err := env.AdminSvc.List(context.Background(), repoQuery())
		require.Error(t, err)
	})

	t.Run("toItem KB 计数错误", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		env.SeedUser(t, "a", "password8", false)
		env.Users.ErrOnKBCount = boom
		_, _, err := env.AdminSvc.List(context.Background(), repoQuery())
		require.Error(t, err)
	})

	t.Run("SetStatus CountAdmins 失败", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		admin := env.SeedUser(t, "root", "password8", true)
		_, target, err := env.AdminSvc.Create(context.Background(), admin.ID, "t", "password8", true, "ip")
		require.NoError(t, err)
		env.Users.ErrOnCountAdmins = boom
		err = env.AdminSvc.SetStatus(context.Background(), admin.ID, target.ID, true, "ip")
		require.Error(t, err)
		err = env.AdminSvc.SetAdmin(context.Background(), admin.ID, target.ID, false, "ip")
		require.Error(t, err)
		env.Users.ErrOnCountAdmins = nil
	})

	t.Run("SetStatus Save 失败（禁用与启用两分支）", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		admin := env.SeedUser(t, "root", "password8", true)
		_, target, _ := env.AdminSvc.Create(context.Background(), admin.ID, "t2", "password8", false, "ip")
		env.Users.ErrOnSave = boom
		require.Error(t, env.AdminSvc.SetStatus(context.Background(), admin.ID, target.ID, true, "ip"))
		require.Error(t, env.AdminSvc.SetStatus(context.Background(), admin.ID, target.ID, false, "ip"))
	})

	t.Run("SetAdmin Save 失败", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		admin := env.SeedUser(t, "root", "password8", true)
		_, target, _ := env.AdminSvc.Create(context.Background(), admin.ID, "t3", "password8", false, "ip")
		env.Users.ErrOnSave = boom
		require.Error(t, env.AdminSvc.SetAdmin(context.Background(), admin.ID, target.ID, true, "ip"))
	})

	t.Run("ResetPassword Save 失败", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		admin := env.SeedUser(t, "root", "password8", true)
		_, target, _ := env.AdminSvc.Create(context.Background(), admin.ID, "t4", "password8", false, "ip")
		env.Users.ErrOnSave = boom
		_, err := env.AdminSvc.ResetPassword(context.Background(), admin.ID, target.ID, "ip")
		require.Error(t, err)
	})

	t.Run("ResetPassword RandomPassword 之外的正常路径已覆盖", func(t *testing.T) {
		env := testutil.NewTestEnv(t, nil)
		admin := env.SeedUser(t, "root", "password8", true)
		_, target, _ := env.AdminSvc.Create(context.Background(), admin.ID, "t5", "password8", false, "ip")
		pw, err := env.AdminSvc.ResetPassword(context.Background(), admin.ID, target.ID, "ip")
		require.NoError(t, err)
		assert.Len(t, pw, 12)
	})
}

func repoQuery() repo.UserListQuery { return repo.UserListQuery{Page: 1, PerPage: 20} }
