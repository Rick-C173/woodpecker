//go:build integration

// UserRepo / KBRepo / ModelCfgRepo 真库集成测试（M1-M3 仓储方法分支）。
package repo

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
)

func itSeedUser(t *testing.T, r *UserRepo, name string, isAdmin bool) *model.User {
	t.Helper()
	u := &model.User{Username: name, PasswordHash: "x", IsAdmin: isAdmin, Status: model.UserStatusActive}
	require.NoError(t, r.Create(context.Background(), u))
	return u
}

func TestUserRepoIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r := NewUserRepo(itDB)

	u1 := itSeedUser(t, r, "alice", true)
	u2 := itSeedUser(t, r, "bob", false)
	require.Greater(t, u2.ID, u1.ID)

	got, err := r.GetByUsername(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, u1.ID, got.ID)
	_, err = r.GetByUsername(ctx, "nobody")
	require.ErrorIs(t, err, ErrNotFound)
	got, err = r.GetByID(ctx, u2.ID)
	require.NoError(t, err)
	assert.Equal(t, "bob", got.Username)
	_, err = r.GetByID(ctx, 99999)
	require.ErrorIs(t, err, ErrNotFound)

	n, err := r.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	n, err = r.CountAdmins(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	u2.Status = model.UserStatusDisabled
	u2.TokenVersion++
	require.NoError(t, r.Save(ctx, u2))
	got, err = r.GetByID(ctx, u2.ID)
	require.NoError(t, err)
	assert.Equal(t, model.UserStatusDisabled, got.Status)
	assert.Equal(t, 1, got.TokenVersion)

	users, total, err := r.List(ctx, UserListQuery{Keyword: "a", Page: 1, PerPage: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, users, 1)
	users, total, err = r.List(ctx, UserListQuery{Status: model.UserStatusDisabled, Page: 1, PerPage: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, users, 1)
	assert.Equal(t, "bob", users[0].Username)
}

func TestKBRepoIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r := NewKBRepo(itDB)
	users := NewUserRepo(itDB)
	owner := itSeedUser(t, users, "kbowner", true)
	member := itSeedUser(t, users, "kbmember", false)

	kb := &model.KnowledgeBase{Name: "订单文档", Description: "描述", CreatedBy: owner.ID}
	require.NoError(t, r.Create(ctx, kb))
	require.Greater(t, kb.ID, int64(0))
	got, err := r.GetByID(ctx, kb.ID)
	require.NoError(t, err)
	assert.Equal(t, "订单文档", got.Name)
	assert.Equal(t, "描述", got.Description)
	_, err = r.GetByID(ctx, 99999)
	require.ErrorIs(t, err, ErrNotFound)
	got, err = r.GetByName(ctx, "订单文档")
	require.NoError(t, err)
	assert.Equal(t, kb.ID, got.ID)

	require.NoError(t, r.UpsertMember(ctx, &model.KBMember{KBID: kb.ID, UserID: owner.ID, Role: model.KBRoleOwner}))
	require.NoError(t, r.UpsertMember(ctx, &model.KBMember{KBID: kb.ID, UserID: member.ID, Role: model.KBRoleMember}))
	role, err := r.GetMemberRole(ctx, kb.ID, member.ID)
	require.NoError(t, err)
	assert.Equal(t, model.KBRoleMember, role)
	_, err = r.GetMemberRole(ctx, kb.ID, 99999)
	require.ErrorIs(t, err, ErrNotFound)
	members, err := r.ListMembers(ctx, kb.ID)
	require.NoError(t, err)
	require.Len(t, members, 2)
	assert.Equal(t, "kbowner", members[0].Username)
	require.NoError(t, r.UpsertMember(ctx, &model.KBMember{KBID: kb.ID, UserID: member.ID, Role: model.KBRoleEditor}))
	role, _ = r.GetMemberRole(ctx, kb.ID, member.ID)
	assert.Equal(t, model.KBRoleEditor, role)
	ok, err := r.UserExists(ctx, member.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = r.UserExists(ctx, 99999)
	require.NoError(t, err)
	assert.False(t, ok)

	rows, err := r.ListForUser(ctx, owner.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, model.KBRoleOwner, rows[0].Role)
	assert.Equal(t, int64(0), rows[0].DocCount)
	assert.Equal(t, int64(0), itCount(t, "SELECT COUNT(*) FROM documents WHERE kb_id = "+strconv.FormatInt(kb.ID, 10)))
	all, err := r.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	got.Name = "订单文档 v2"
	require.NoError(t, r.Save(ctx, got))
	got, _ = r.GetByID(ctx, kb.ID)
	assert.Equal(t, "订单文档 v2", got.Name)

	require.NoError(t, r.DeleteMember(ctx, kb.ID, member.ID))
	_, err = r.GetMemberRole(ctx, kb.ID, member.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestModelCfgRepoIT(t *testing.T) {
	itTruncate(t)
	ctx := context.Background()
	r := NewModelCfgRepo(itDB)

	svc := &model.ModelService{Name: "deepseek", BaseURL: "https://api.deepseek.com", APIKeyEnc: []byte("cipher"), Enabled: true}
	require.NoError(t, r.CreateService(ctx, svc))
	require.Greater(t, svc.ID, int64(0))
	got, err := r.GetServiceByID(ctx, svc.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("cipher"), got.APIKeyEnc)
	_, err = r.GetServiceByID(ctx, 99999)
	require.ErrorIs(t, err, ErrNotFound)

	e1 := &model.ModelEntry{ServiceID: svc.ID, Purpose: model.ModelPurposeChat, ModelName: "deepseek-chat", IsDefault: true, Params: []byte("{}")}
	require.NoError(t, r.CreateEntry(ctx, e1))
	e2 := &model.ModelEntry{ServiceID: svc.ID, Purpose: model.ModelPurposeEmbedding, ModelName: "bge-m3", Params: []byte("{}")}
	require.NoError(t, r.CreateEntry(ctx, e2))

	entries, err := r.ListEntries(ctx, svc.ID, model.ModelPurposeChat)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "deepseek-chat", entries[0].ModelName)
	entries, err = r.ListEntries(ctx, svc.ID, 0)
	require.NoError(t, err)
	assert.Len(t, entries, 2)

	defEntry, defSvc, err := r.GetDefaultEntry(ctx, model.ModelPurposeChat)
	require.NoError(t, err)
	assert.Equal(t, e1.ID, defEntry.ID)
	assert.Equal(t, "deepseek", defSvc.Name)

	e3 := &model.ModelEntry{ServiceID: svc.ID, Purpose: model.ModelPurposeChat, ModelName: "deepseek-reasoner", Params: []byte("{}")}
	require.NoError(t, r.CreateEntry(ctx, e3))
	require.NoError(t, r.SetDefaultEntry(ctx, model.ModelPurposeChat, e3.ID))
	defEntry, _, err = r.GetDefaultEntry(ctx, model.ModelPurposeChat)
	require.NoError(t, err)
	assert.Equal(t, e3.ID, defEntry.ID)
	entries, _ = r.ListEntries(ctx, svc.ID, model.ModelPurposeChat)
	defaults := 0
	for _, e := range entries {
		if e.IsDefault {
			defaults++
		}
	}
	assert.Equal(t, 1, defaults, "每用途仅一个默认")

	n, err := r.CountEntriesByService(ctx, svc.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	svc.Enabled = false
	require.NoError(t, r.SaveService(ctx, svc))
	_, _, err = r.GetDefaultEntry(ctx, model.ModelPurposeChat)
	require.ErrorIs(t, err, ErrNotFound, "停用服务的默认条目不可解析")
	svc.Enabled = true
	require.NoError(t, r.SaveService(ctx, svc))
	services, err := r.ListServices(ctx)
	require.NoError(t, err)
	require.Len(t, services, 1)
	assert.Equal(t, int64(3), services[0].EntryCount)
	require.NoError(t, r.DeleteEntry(ctx, e3.ID))
	_, err = r.GetEntryByID(ctx, e3.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, r.DeleteService(ctx, svc.ID))
	_, err = r.GetServiceByID(ctx, svc.ID)
	require.ErrorIs(t, err, ErrNotFound)
}
