//go:build integration

// 仓储层真库集成补充（M1-M4）：UserRepo / KBRepo / ModelCfgRepo / DocumentRepo。
// 与 m1-m3 的 HTTP 流测试互补，覆盖 repo 方法分支，提高集成跨包覆盖率。
package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

func seedUser(t *testing.T, r *repo.UserRepo, name string, isAdmin bool) *model.User {
	t.Helper()
	u := &model.User{Username: name, PasswordHash: "x", IsAdmin: isAdmin, Status: model.UserStatusActive}
	require.NoError(t, r.Create(context.Background(), u))
	return u
}

func TestUserRepo(t *testing.T) {
	db := openTestDB(t)
	truncateAll(db)
	ctx := context.Background()
	r := repo.NewUserRepo(db)

	u1 := seedUser(t, r, "alice", true)
	u2 := seedUser(t, r, "bob", false)
	require.Greater(t, u2.ID, u1.ID)

	// GetByUsername / GetByID / NotFound
	got, err := r.GetByUsername(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, u1.ID, got.ID)
	_, err = r.GetByUsername(ctx, "nobody")
	require.ErrorIs(t, err, repo.ErrNotFound)
	got, err = r.GetByID(ctx, u2.ID)
	require.NoError(t, err)
	assert.Equal(t, "bob", got.Username)
	_, err = r.GetByID(ctx, 99999)
	require.ErrorIs(t, err, repo.ErrNotFound)

	// Count / CountAdmins
	assert.Equal(t, int64(2), mustCount(t, func() (int64, error) { return r.Count(ctx) }))
	assert.Equal(t, int64(1), mustCount(t, func() (int64, error) { return r.CountAdmins(ctx) }))

	// Save（禁用 + 版本号提升）
	u2.Status = model.UserStatusDisabled
	u2.TokenVersion++
	require.NoError(t, r.Save(ctx, u2))
	got, err = r.GetByID(ctx, u2.ID)
	require.NoError(t, err)
	assert.Equal(t, model.UserStatusDisabled, got.Status)
	assert.Equal(t, 1, got.TokenVersion)

	// List：keyword + status 过滤 + 分页
	users, total, err := r.List(ctx, repo.UserListQuery{Keyword: "a", Page: 1, PerPage: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, users, 1)
	users, total, err = r.List(ctx, repo.UserListQuery{Status: model.UserStatusDisabled, Page: 1, PerPage: 10})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, users, 1)
	assert.Equal(t, "bob", users[0].Username)
}

func TestKBRepo(t *testing.T) {
	db := openTestDB(t)
	truncateAll(db)
	ctx := context.Background()
	r := repo.NewKBRepo(db)
	users := repo.NewUserRepo(db)
	owner := seedUser(t, users, "kbowner", true)
	member := seedUser(t, users, "kbmember", false)

	// Create / GetByID / GetByName
	kb := &model.KnowledgeBase{Name: "订单文档", Description: "描述", CreatedBy: owner.ID}
	require.NoError(t, r.Create(ctx, kb))
	require.Greater(t, kb.ID, int64(0))
	got, err := r.GetByID(ctx, kb.ID)
	require.NoError(t, err)
	assert.Equal(t, "订单文档", got.Name)
	assert.Equal(t, "描述", got.Description)
	_, err = r.GetByID(ctx, 99999)
	require.ErrorIs(t, err, repo.ErrNotFound)
	got, err = r.GetByName(ctx, "订单文档")
	require.NoError(t, err)
	assert.Equal(t, kb.ID, got.ID)

	// 成员：upsert → 角色查询 → 列表 → 变更 → 移除
	require.NoError(t, r.UpsertMember(ctx, &model.KBMember{KBID: kb.ID, UserID: owner.ID, Role: model.KBRoleOwner}))
	require.NoError(t, r.UpsertMember(ctx, &model.KBMember{KBID: kb.ID, UserID: member.ID, Role: model.KBRoleMember}))
	role, err := r.GetMemberRole(ctx, kb.ID, member.ID)
	require.NoError(t, err)
	assert.Equal(t, model.KBRoleMember, role)
	_, err = r.GetMemberRole(ctx, kb.ID, 99999)
	require.ErrorIs(t, err, repo.ErrNotFound)
	members, err := r.ListMembers(ctx, kb.ID)
	require.NoError(t, err)
	require.Len(t, members, 2)
	assert.Equal(t, "kbowner", members[0].Username)
	// 角色变更（同 key upsert）
	require.NoError(t, r.UpsertMember(ctx, &model.KBMember{KBID: kb.ID, UserID: member.ID, Role: model.KBRoleEditor}))
	role, _ = r.GetMemberRole(ctx, kb.ID, member.ID)
	assert.Equal(t, model.KBRoleEditor, role)
	// UserExists
	ok, err := r.UserExists(ctx, member.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = r.UserExists(ctx, 99999)
	require.NoError(t, err)
	assert.False(t, ok)

	// 列表（含 my_role 与文档计数）+ 计数
	rows, err := r.ListForUser(ctx, owner.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, model.KBRoleOwner, rows[0].Role)
	assert.Equal(t, int64(0), rows[0].DocCount)
	assert.Equal(t, int64(0), mustCount(t, func() (int64, error) { return r.CountDocuments(ctx, kb.ID) }))
	all, err := r.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	// Save
	got.Name = "订单文档 v2"
	require.NoError(t, r.Save(ctx, got))
	got, _ = r.GetByID(ctx, kb.ID)
	assert.Equal(t, "订单文档 v2", got.Name)

	// DeleteMember → 移除后角色查询 NotFound
	require.NoError(t, r.DeleteMember(ctx, kb.ID, member.ID))
	_, err = r.GetMemberRole(ctx, kb.ID, member.ID)
	require.ErrorIs(t, err, repo.ErrNotFound)
}

func TestModelCfgRepo(t *testing.T) {
	db := openTestDB(t)
	truncateAll(db)
	ctx := context.Background()
	r := repo.NewModelCfgRepo(db)

	// 服务 CRUD
	svc := &model.ModelService{Name: "deepseek", BaseURL: "https://api.deepseek.com", APIKeyEnc: []byte("cipher"), Enabled: true}
	require.NoError(t, r.CreateService(ctx, svc))
	require.Greater(t, svc.ID, int64(0))
	got, err := r.GetServiceByID(ctx, svc.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("cipher"), got.APIKeyEnc)
	_, err = r.GetServiceByID(ctx, 99999)
	require.ErrorIs(t, err, repo.ErrNotFound)

	// 条目：chat 默认 + embedding（params 为 jsonb NOT NULL，至少存 {}）
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

	// GetDefaultEntry：默认 + 启用中服务
	defEntry, defSvc, err := r.GetDefaultEntry(ctx, model.ModelPurposeChat)
	require.NoError(t, err)
	assert.Equal(t, e1.ID, defEntry.ID)
	assert.Equal(t, "deepseek", defSvc.Name)

	// SetDefaultEntry 切换：每用途唯一（部分唯一索引）
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

	// 计数 / 服务停用后默认不可解析 / 删除
	assert.Equal(t, int64(3), mustCount(t, func() (int64, error) { return r.CountEntriesByService(ctx, svc.ID) }))
	svc.Enabled = false
	require.NoError(t, r.SaveService(ctx, svc))
	_, _, err = r.GetDefaultEntry(ctx, model.ModelPurposeChat)
	require.ErrorIs(t, err, repo.ErrNotFound, "停用服务的默认条目不可解析")
	svc.Enabled = true
	require.NoError(t, r.SaveService(ctx, svc))
	services, err := r.ListServices(ctx)
	require.NoError(t, err)
	require.Len(t, services, 1)
	assert.Equal(t, int64(3), services[0].EntryCount)
	require.NoError(t, r.DeleteEntry(ctx, e3.ID))
	_, err = r.GetEntryByID(ctx, e3.ID)
	require.ErrorIs(t, err, repo.ErrNotFound)
	require.NoError(t, r.DeleteService(ctx, svc.ID))
	_, err = r.GetServiceByID(ctx, svc.ID)
	require.ErrorIs(t, err, repo.ErrNotFound)
}

func TestDocumentRepo(t *testing.T) {
	db := openTestDB(t)
	truncateAll(db)
	ctx := context.Background()
	r := repo.NewDocumentRepo(db)
	kbs := repo.NewKBRepo(db)
	users := repo.NewUserRepo(db)
	owner := seedUser(t, users, "docowner", true)
	kb := &model.KnowledgeBase{Name: "文档库", CreatedBy: owner.ID}
	require.NoError(t, kbs.Create(ctx, kb))

	// 文档 CRUD
	doc := &model.Document{KBID: kb.ID, Type: model.DocTypeGit, Name: "order-service",
		Status: model.DocStatusProcessing, CreatedBy: owner.ID}
	require.NoError(t, r.CreateDocument(ctx, doc))
	require.Greater(t, doc.ID, int64(0))
	got, err := r.GetDocumentByID(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, model.DocStatusProcessing, got.Status)
	_, err = r.GetDocumentByID(ctx, 99999)
	require.ErrorIs(t, err, repo.ErrNotFound)
	assert.Equal(t, int64(1), mustCount(t, func() (int64, error) { return r.CountByKB(ctx, kb.ID) }))

	// 列表：类型/状态过滤 + 分页
	rows, total, err := r.ListDocuments(ctx, repo.DocumentListQuery{KBID: kb.ID, Type: model.DocTypeGit, Status: model.DocStatusProcessing, Page: 1, PerPage: 20})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)

	// 文件清单
	f1 := &model.DocumentFile{DocumentID: doc.ID, Path: "docs/api.md", Status: 1, Size: ptrI64(120)}
	require.NoError(t, r.UpsertFile(ctx, f1))
	files, err := r.ListFiles(ctx, doc.ID)
	require.NoError(t, err)
	require.Len(t, files, 1)
	fg, err := r.GetFileByID(ctx, f1.ID)
	require.NoError(t, err)
	assert.Equal(t, "docs/api.md", fg.Path)
	require.NoError(t, r.DeleteFilesByPath(ctx, doc.ID, []string{"docs/api.md"}))
	files, _ = r.ListFiles(ctx, doc.ID)
	assert.Empty(t, files)

	// 分块替换 / 按文件删除（embedding 列 NOT NULL，填测试向量）
	chunks := []model.Chunk{
		{DocumentID: doc.ID, KBID: kb.ID, Content: "块一", ChunkIndex: 0, FilePath: ptrStr7("docs/api.md"), Embedding: unitVec(0.9)},
		{DocumentID: doc.ID, KBID: kb.ID, Content: "块二", ChunkIndex: 1, FilePath: ptrStr7("docs/other.md"), Embedding: unitVec(0.2)},
	}
	require.NoError(t, r.ReplaceChunks(ctx, doc.ID, chunks))
	assert.Equal(t, int64(2), m6count(t, db, "SELECT COUNT(*) FROM chunks WHERE document_id = "+i64(doc.ID)))
	require.NoError(t, r.DeleteChunksByFile(ctx, doc.ID, "docs/api.md"))
	assert.Equal(t, int64(1), m6count(t, db, "SELECT COUNT(*) FROM chunks WHERE document_id = "+i64(doc.ID)))

	// 任务：幂等判定 / 历史 / 僵尸清理
	task := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull, Status: model.TaskStatusQueued}
	require.NoError(t, r.CreateTask(ctx, task))
	running, err := r.HasRunningTask(ctx, doc.ID)
	require.NoError(t, err)
	assert.True(t, running)
	other := &model.Document{KBID: kb.ID, Type: model.DocTypeUpload, Name: "a.md", Status: model.DocStatusReady, CreatedBy: owner.ID}
	require.NoError(t, r.CreateDocument(ctx, other))
	running, err = r.HasRunningTask(ctx, other.ID)
	require.NoError(t, err)
	assert.False(t, running)
	task.Status = model.TaskStatusSuccess
	require.NoError(t, r.SaveTask(ctx, task))
	gotTask, err := r.GetTaskByID(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatusSuccess, gotTask.Status)
	hist, err := r.ListTaskHistory(ctx, doc.ID, 10)
	require.NoError(t, err)
	require.Len(t, hist, 1)
	assert.Equal(t, model.TaskStatusSuccess, hist[0].Status)

	// 僵尸任务 / 处理中文档清理
	z1 := &model.IngestTask{DocumentID: doc.ID, Type: model.TaskTypeFull, Status: model.TaskStatusRunning}
	require.NoError(t, r.CreateTask(ctx, z1))
	n, err := r.FailZombieTasks(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	n, err = r.FailProcessingDocs(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "processing 文档置失败")
	got, _ = r.GetDocumentByID(ctx, doc.ID)
	assert.Equal(t, model.DocStatusFailed, got.Status)

	// 删除文档
	require.NoError(t, r.DeleteDocument(ctx, doc.ID))
	_, err = r.GetDocumentByID(ctx, doc.ID)
	require.ErrorIs(t, err, repo.ErrNotFound)
}

func ptrStr7(s string) *string { return &s }
func ptrI64(v int64) *int64    { return &v }

func mustCount(t *testing.T, fn func() (int64, error)) int64 {
	t.Helper()
	v, err := fn()
	require.NoError(t, err)
	return v
}
