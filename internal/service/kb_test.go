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

	item, err := env.KBSvc.Create(ctx, root.ID, "  平台文档  ", "内部平台", 0, "1.1.1.1")
	require.NoError(t, err)
	assert.Equal(t, "平台文档", item.Name) // 首尾空白被裁剪
	assert.Equal(t, "admin", item.MyRole)
	assert.Equal(t, int64(0), item.DocumentCount)
	assert.Equal(t, 1, env.Audit.CountAction("kb.create"))

	// 创建者默认落 kb_members owner 行（端到端验证发现的隐患修复）
	role, err := env.KBs.GetMemberRole(ctx, item.ID, root.ID)
	require.NoError(t, err)
	assert.Equal(t, model.KBRoleOwner, role)

	// 重名 → KB_001
	_, err = env.KBSvc.Create(ctx, root.ID, "平台文档", "", 0, "")
	assert.Equal(t, service.CodeKBNameExists, svcErr(t, err).Code)

	// 名称校验
	_, err = env.KBSvc.Create(ctx, root.ID, "   ", "", 0, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = env.KBSvc.Create(ctx, root.ID, strings.Repeat("a", 51), "", 0, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = env.KBSvc.Create(ctx, root.ID, "合法名", strings.Repeat("d", 501), 0, "")
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
	kb1, err := env.KBSvc.Create(ctx, root.ID, "库一", "", 0, "")
	require.NoError(t, err)
	kb2, err := env.KBSvc.Create(ctx, root.ID, "库二", "", 0, "")
	require.NoError(t, err)

	// 重名 → KB_001；同名（未变化）允许
	name := "库二"
	_, err = env.KBSvc.Update(ctx, root.ID, kb1.ID, &name, nil, nil, "")
	assert.Equal(t, service.CodeKBNameExists, svcErr(t, err).Code)
	same := "库一"
	_, err = env.KBSvc.Update(ctx, root.ID, kb1.ID, &same, nil, nil, "")
	require.NoError(t, err)

	// 名称/描述校验
	empty := ""
	_, err = env.KBSvc.Update(ctx, root.ID, kb1.ID, &empty, nil, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	long := strings.Repeat("x", 501)
	_, err = env.KBSvc.Update(ctx, root.ID, kb1.ID, nil, &long, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 正常改名 + 改描述
	newName := "库一-改"
	desc := "新描述"
	item, err := env.KBSvc.Update(ctx, root.ID, kb1.ID, &newName, &desc, nil, "ip")
	require.NoError(t, err)
	assert.Equal(t, "库一-改", item.Name)
	assert.Equal(t, "新描述", item.Description)
	assert.Equal(t, 2, env.Audit.CountAction("kb.update")) // 含前面同名空更新

	// 不存在
	_, err = env.KBSvc.Update(ctx, root.ID, 9999, &newName, nil, nil, "")
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
	kb, err := env.KBSvc.Create(ctx, root.ID, "协作库", "", 0, "")
	require.NoError(t, err)

	// 参数与目标校验
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, dev.ID, "boss", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, 0, "member", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, 9999, "member", "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)

	// 添加 → 成员列表（创建者 owner 行在前，role ASC）
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, dev.ID, "editor", "ip")
	require.NoError(t, err)
	assert.Equal(t, 1, env.Audit.CountAction("member.grant"))
	members, err := env.KBSvc.ListMembers(ctx, kb.ID)
	require.NoError(t, err)
	require.Len(t, members, 2)
	assert.Equal(t, "root", members[0].Username)
	assert.Equal(t, "owner", members[0].Role)
	assert.Equal(t, "dev", members[1].Username)
	assert.Equal(t, "editor", members[1].Role)

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
	assert.Equal(t, 1, env.KBs.MemberCount(kb.ID)) // 仅剩创建者 owner 行
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
	kb, err := env.KBSvc.Create(ctx, root.ID, "错误库", "", 0, "")
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
	_, err = env.KBSvc.Create(ctx, root.ID, "新库", "", 0, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnGet = nil

	// Create 写入失败
	env.KBs.ErrOnCreate = injected
	_, err = env.KBSvc.Create(ctx, root.ID, "新库", "", 0, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnCreate = nil

	// 读取失败（Get / Update / Delete / 成员入口）
	env.KBs.ErrOnGet = injected
	_, err = env.KBSvc.Get(ctx, kb.ID, root.ID, true)
	require.ErrorIs(t, err, injected)
	_, err = env.KBSvc.Update(ctx, root.ID, kb.ID, nil, nil, nil, "")
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
	_, err = env.KBSvc.Update(ctx, root.ID, kb.ID, &name, nil, nil, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnGet = nil

	// Save / Delete / CountDocuments
	env.KBs.ErrOnSave = injected
	_, err = env.KBSvc.Update(ctx, root.ID, kb.ID, nil, nil, nil, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnSave = nil
	env.KBs.ErrOnDocCount = injected
	_, err = env.KBSvc.Get(ctx, kb.ID, root.ID, true)
	require.ErrorIs(t, err, injected)
	_, err = env.KBSvc.Update(ctx, root.ID, kb.ID, nil, nil, nil, "")
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

	// Create 后写 owner 成员行失败 → 补偿删除，不留半成品库
	env.KBs.ErrOnMembers = injected
	_, err = env.KBSvc.Create(ctx, root.ID, "补偿库", "", 0, "")
	require.ErrorIs(t, err, injected)
	_, gerr := env.KBs.GetByName(ctx, "补偿库")
	require.Error(t, gerr)
	env.KBs.ErrOnMembers = nil

	// 用户存在性查询失败
	env.KBs.ErrOnUserExists = injected
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, dev.ID, "member", "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnUserExists = nil
}

// TestKBMemberErrorPaths 成员角色查询/删除失败与审计写入失败（不抛错）。
func TestKBMemberErrorPaths(t *testing.T) {
	env, root, dev := kbEnv(t)
	ctx := context.Background()
	kb, err := env.KBSvc.Create(ctx, root.ID, "成员错误库", "", 0, "")
	require.NoError(t, err)
	injected := errors.New("member store down")
	_, err = env.KBSvc.AddMember(ctx, root.ID, kb.ID, dev.ID, "editor", "")
	require.NoError(t, err)

	env.KBs.ErrOnMembers = injected
	// 角色查询失败 → 透传（UpdateMemberRole / RemoveMember）
	env.KBs.ErrOnRole = injected
	err = env.KBSvc.UpdateMemberRole(ctx, root.ID, kb.ID, dev.ID, "member", "")
	require.ErrorIs(t, err, injected)
	err = env.KBSvc.RemoveMember(ctx, root.ID, kb.ID, dev.ID, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnRole = nil

	// 删除成员失败
	err = env.KBSvc.RemoveMember(ctx, root.ID, kb.ID, dev.ID, "")
	require.ErrorIs(t, err, injected)
	env.KBs.ErrOnMembers = nil

	// 审计写入失败不抛错（静默降级）
	env.Audit.FailNext = true
	require.NoError(t, env.KBSvc.RemoveMember(ctx, root.ID, kb.ID, dev.ID, "ip"))
	assert.Equal(t, 1, env.KBs.MemberCount(kb.ID)) // 仅剩创建者 owner 行
}

// TestKBVisibility FR-17 可见性：建库取值校验、切换审计、org 库列表/详情对非成员可见。
func TestKBVisibility(t *testing.T) {
	env, root, dev := kbEnv(t)
	ctx := context.Background()

	// 1. 缺省（0）→ 私有；显式 2 → 全员可见；3 → 422
	priv, err := env.KBSvc.Create(ctx, root.ID, "私有库", "", 0, "")
	require.NoError(t, err)
	assert.Equal(t, model.KBVisibilityPrivate, priv.Visibility)

	pub, err := env.KBSvc.Create(ctx, root.ID, "公开库", "", model.KBVisibilityOrg, "")
	require.NoError(t, err)
	assert.Equal(t, model.KBVisibilityOrg, pub.Visibility)

	_, err = env.KBSvc.Create(ctx, root.ID, "非法库", "", 3, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 2. Update 切换可见性：1→2→1（切回）→非法 422
	_, err = env.KBSvc.Update(ctx, root.ID, priv.ID, nil, nil, ptr16(model.KBVisibilityOrg), "")
	require.NoError(t, err)
	got, err := env.KBSvc.Get(ctx, priv.ID, root.ID, true)
	require.NoError(t, err)
	assert.Equal(t, model.KBVisibilityOrg, got.Visibility)
	_, err = env.KBSvc.Update(ctx, root.ID, priv.ID, nil, nil, ptr16(model.KBVisibilityPrivate), "")
	require.NoError(t, err)
	got, err = env.KBSvc.Get(ctx, priv.ID, root.ID, true)
	require.NoError(t, err)
	assert.Equal(t, model.KBVisibilityPrivate, got.Visibility)
	_, err = env.KBSvc.Update(ctx, root.ID, priv.ID, nil, nil, ptr16(3), "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 3. 列表：org 库对非成员可见（my_role=org）；私有库不可见；成员库角色正常
	items, err := env.KBSvc.List(ctx, dev.ID, false)
	require.NoError(t, err)
	byID := map[int64]service.KBItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	orgRow, ok := byID[pub.ID]
	require.True(t, ok, "全员可见库应在普通用户列表中")
	assert.Equal(t, "org", orgRow.MyRole)
	assert.Equal(t, model.KBVisibilityOrg, orgRow.Visibility)
	_, ok = byID[priv.ID]
	assert.False(t, ok, "私有库对非成员不可见")

	// 4. 详情：org 库非成员可读（my_role=org），私有库非成员 404
	orgDetail, err := env.KBSvc.Get(ctx, pub.ID, dev.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "org", orgDetail.MyRole)
	_, err = env.KBSvc.Get(ctx, priv.ID, dev.ID, false)
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)

	// 5. 审计：kb.update 含 visibility 变更
	assert.GreaterOrEqual(t, env.Audit.CountAction("kb.update"), 1)
}

func ptr16(v int16) *int16 { return &v }
