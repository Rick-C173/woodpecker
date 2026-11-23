package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

func kbEnv(t *testing.T) (*testutil.TestEnv, *model.User, *model.User) {
	t.Helper()
	env := testutil.NewTestEnv(t, nil)
	root := env.SeedUser(t, "root", "password8", true)
	dev := env.SeedUser(t, "dev", "password8", false)
	return env, root, dev
}

func TestKBCreateAndList(t *testing.T) {
	env, root, dev := kbEnv(t)
	ctx := context.Background()

	item, err := env.KBSvc.Create(ctx, root.ID, "  平台文档  ", "内部平台", "1.1.1.1")
	require.NoError(t, err)
	assert.Equal(t, "平台文档", item.Name) // 首尾空白被裁剪
	assert.Equal(t, "admin", item.MyRole)
	assert.Equal(t, int64(0), item.DocumentCount)
	assert.Equal(t, 1, env.Audit.CountAction("kb.create"))

	// 重名 → KB_001
	_, err = env.KBSvc.Create(ctx, root.ID, "平台文档", "", "")
	assert.Equal(t, service.CodeKBNameExists, svcErr(t, err).Code)

	// 名称校验
	_, err = env.KBSvc.Create(ctx, root.ID, "   ", "", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = env.KBSvc.Create(ctx, root.ID, strings.Repeat("a", 51), "", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = env.KBSvc.Create(ctx, root.ID, "合法名", strings.Repeat("d", 501), "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 管理员看到全部
	items, err := env.KBSvc.List(ctx, root.ID, true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "admin", items[0].MyRole)

	// 普通用户：未授权 → 空列表
	items, err = env.KBSvc.List(ctx, dev.ID, false)
	require.NoError(t, err)
	assert.Empty(t, items)

	// 授权后列表带角色与文档数
	env.KBs.SetRole(item.ID, dev.ID, model.KBRoleEditor)
	env.KBs.SetDocCount(item.ID, 3)
	items, err = env.KBSvc.List(ctx, dev.ID, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "editor", items[0].MyRole)
	assert.Equal(t, int64(3), items[0].DocumentCount)
}

func TestKBUpdateAndGet(t *testing.T) {
	env, root, dev := kbEnv(t)
	ctx := context.Background()
	kb1, err := env.KBSvc.Create(ctx, root.ID, "库一", "", "")
	require.NoError(t, err)
	kb2, err := env.KBSvc.Create(ctx, root.ID, "库二", "", "")
	require.NoError(t, err)

	// 重名 → KB_001；同名（未变化）允许
	name := "库二"
	_, err = env.KBSvc.Update(ctx, root.ID, kb1.ID, &name, nil, "")
	assert.Equal(t, service.CodeKBNameExists, svcErr(t, err).Code)
	same := "库一"
	_, err = env.KBSvc.Update(ctx, root.ID, kb1.ID, &same, nil, "")
	require.NoError(t, err)

	// 名称/描述校验
	empty := ""
	_, err = env.KBSvc.Update(ctx, root.ID, kb1.ID, &empty, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	long := strings.Repeat("x", 501)
	_, err = env.KBSvc.Update(ctx, root.ID, kb1.ID, nil, &long, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 正常改名 + 改描述
	newName := "库一-改"
	desc := "新描述"
	item, err := env.KBSvc.Update(ctx, root.ID, kb1.ID, &newName, &desc, "ip")
	require.NoError(t, err)
	assert.Equal(t, "库一-改", item.Name)
	assert.Equal(t, "新描述", item.Description)
	assert.Equal(t, 2, env.Audit.CountAction("kb.update")) // 含前面同名空更新

	// 不存在
	_, err = env.KBSvc.Update(ctx, root.ID, 9999, &newName, nil, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	_, err = env.KBSvc.Get(ctx, 9999, root.ID, true)
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)

	// Get：非成员 404；成员带角色；管理员 admin
	_, err = env.KBSvc.Get(ctx, kb2.ID, dev.ID, false)
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	env.KBs.SetRole(kb2.ID, dev.ID, model.KBRoleMember)
	got, err := env.KBSvc.Get(ctx, kb2.ID, dev.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "member", got.MyRole)
	got, err = env.KBSvc.Get(ctx, kb2.ID, root.ID, true)
	require.NoError(t, err)
	assert.Equal(t, "admin", got.MyRole)

	// 删除
	require.NoError(t, env.KBSvc.Delete(ctx, root.ID, kb1.ID, "ip"))
	assert.Equal(t, 1, env.Audit.CountAction("kb.delete"))
	err = env.KBSvc.Delete(ctx, root.ID, kb1.ID, "ip")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	_, err = env.KBSvc.Get(ctx, kb1.ID, root.ID, true)
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
}

func TestKBMemberLifecycle(t *testing.T) {
	env, root, dev := kbEnv(t)
	ctx := context.Background()
	kb, err := env.KBSvc.Create(ctx, root.ID, "协作库", "", "")
	require.NoError(t, err)

	// 参数与目标校验
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, dev.ID, "boss", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, 0, "member", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, 9999, "member", "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)

	// 添加 → 成员列表
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, dev.ID, "editor", "ip")
	require.NoError(t, err)
	assert.Equal(t, 1, env.Audit.CountAction("member.grant"))
	members, err := env.KBSvc.ListMembers(ctx, kb.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, "dev", members[0].Username)
	assert.Equal(t, "editor", members[0].Role)

	// 角色变更
	err = env.KBSvc.UpdateMemberRole(ctx, root.ID, kb.ID, dev.ID, "boss", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	err = env.KBSvc.UpdateMemberRole(ctx, root.ID, kb.ID, 9999, "owner", "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	require.NoError(t, env.KBSvc.UpdateMemberRole(ctx, root.ID, kb.ID, dev.ID, "owner", "ip"))
	assert.Equal(t, 1, env.Audit.CountAction("member.role_change"))
	role, err := env.KBs.GetMemberRole(ctx, kb.ID, dev.ID)
	require.NoError(t, err)
	assert.Equal(t, model.KBRoleOwner, role)

	// 移除
	err = env.KBSvc.RemoveMember(ctx, root.ID, kb.ID, 9999, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	require.NoError(t, env.KBSvc.RemoveMember(ctx, root.ID, kb.ID, dev.ID, "ip"))
	assert.Equal(t, 1, env.Audit.CountAction("member.remove"))
	assert.Equal(t, 0, env.KBs.MemberCount(kb.ID))
	err = env.KBSvc.RemoveMember(ctx, root.ID, kb.ID, dev.ID, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)

	// 库不存在：各入口统一 404
	_, err = env.KBSvc.ListMembers(ctx, 9999)
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	_, err = env.KBSvc.AddMember(ctx, root.ID, 9999, dev.ID, "member", "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	err = env.KBSvc.UpdateMemberRole(ctx, root.ID, 9999, dev.ID, "member", "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	err = env.KBSvc.RemoveMember(ctx, root.ID, 9999, dev.ID, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
}

func TestKBRepoErrorPaths(t *testing.T) {
	env, root, dev := kbEnv(t)
	ctx := context.Background()
	kb, err := env.KBSvc.Create(ctx, root.ID, "错误库", "", "")
	require.NoError(t, err)
	injected := errors.New("db down")

	// List
	env.KBs.ErrOnList = injected
	_, err = env.KBSvc.List(ctx, root.ID, true)
	require.ErrorIs(t, err, injected)
	_, err = env.KBSvc.List(ctx, dev.ID, false)
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnList = nil

	// Create 前的重名查询失败
	env.KBs.ErrOnGet = injected
	_, err = env.KBSvc.Create(ctx, root.ID, "新库", "", "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnGet = nil

	// Create 写入失败
	env.KBs.ErrOnCreate = injected
	_, err = env.KBSvc.Create(ctx, root.ID, "新库", "", "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnCreate = nil

	// 读取失败（Get / Update / Delete / 成员入口）
	env.KBs.ErrOnGet = injected
	_, err = env.KBSvc.Get(ctx, kb.ID, root.ID, true)
	require.ErrorIs(t, err, injected)
	_, err = env.KBSvc.Update(ctx, root.ID, kb.ID, nil, nil, "")
	require.ErrorIs(t, err, injected)
	err = env.KBSvc.Delete(ctx, root.ID, kb.ID, "")
	require.ErrorIs(t, err, injected)
	_, err = env.KBSvc.ListMembers(ctx, kb.ID)
	require.ErrorIs(t, err, injected)
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, dev.ID, "member", "")
	require.ErrorIs(t, err, injected)
	err = env.KBSvc.UpdateMemberRole(ctx, root.ID, kb.ID, dev.ID, "member", "")
	require.ErrorIs(t, err, injected)
	err = env.KBSvc.RemoveMember(ctx, root.ID, kb.ID, dev.ID, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnGet = nil

	// 改名时重名查询失败（非 NotFound）
	env.KBs.ErrOnGet = injected
	name := "另一个名字"
	_, err = env.KBSvc.Update(ctx, root.ID, kb.ID, &name, nil, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnGet = nil

	// Save / Delete / CountDocuments
	env.KBs.ErrOnSave = injected
	_, err = env.KBSvc.Update(ctx, root.ID, kb.ID, nil, nil, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnSave = nil
	env.KBs.ErrOnDocCount = injected
	_, err = env.KBSvc.Get(ctx, kb.ID, root.ID, true)
	require.ErrorIs(t, err, injected)
	_, err = env.KBSvc.Update(ctx, root.ID, kb.ID, nil, nil, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnDocCount = nil
	env.KBs.ErrOnDelete = injected
	err = env.KBSvc.Delete(ctx, root.ID, kb.ID, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnDelete = nil

	// 成员存储失败
	env.KBs.ErrOnMembers = injected
	_, err = env.KBSvc.ListMembers(ctx, kb.ID)
	require.ErrorIs(t, err, injected)
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, dev.ID, "member", "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnMembers = nil

	// 用户存在性查询失败
	env.KBs.ErrOnUserExists = injected
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, dev.ID, "member", "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnUserExists = nil
}
