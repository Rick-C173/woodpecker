package testutil

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// userGetter 成员校验所需的用户存在性查询（FakeUserRepo 满足）。
type userGetter interface {
	GetByID(ctx context.Context, id int64) (*model.User, error)
}

// FakeKBRepo 知识库存储替身：内存实现，支持错误注入。
type FakeKBRepo struct {
	mu       sync.Mutex
	kbs      map[int64]*model.KnowledgeBase
	members  map[int64]map[int64]int16 // kb_id → user_id → role
	docCount map[int64]int64
	nextID   int64
	Users    userGetter // 可选：校验目标用户是否存在

	ErrOnCreate     error
	ErrOnSave       error
	ErrOnGet        error
	ErrOnDelete     error
	ErrOnList       error
	ErrOnDocCount   error
	ErrOnMembers    error // ListMembers / UpsertMember / DeleteMember
	ErrOnRole       error // GetMemberRole（与 ErrOnMembers 分开注入）
	ErrOnUserExists error
}

func NewFakeKBs() *FakeKBRepo {
	return &FakeKBRepo{
		kbs:      map[int64]*model.KnowledgeBase{},
		members:  map[int64]map[int64]int16{},
		docCount: map[int64]int64{},
		nextID:   1,
	}
}

func (f *FakeKBRepo) Create(_ context.Context, kb *model.KnowledgeBase) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnCreate != nil {
		return f.ErrOnCreate
	}
	kb.ID = f.nextID
	f.nextID++
	now := time.Now()
	kb.CreatedAt = now
	kb.UpdatedAt = now
	cp := *kb
	f.kbs[kb.ID] = &cp
	return nil
}

func (f *FakeKBRepo) Save(_ context.Context, kb *model.KnowledgeBase) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnSave != nil {
		return f.ErrOnSave
	}
	if _, ok := f.kbs[kb.ID]; !ok {
		return repo.ErrNotFound
	}
	cp := *kb
	f.kbs[kb.ID] = &cp
	return nil
}

func (f *FakeKBRepo) GetByID(_ context.Context, id int64) (*model.KnowledgeBase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnGet != nil {
		return nil, f.ErrOnGet
	}
	kb, ok := f.kbs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *kb
	return &cp, nil
}

func (f *FakeKBRepo) GetByName(_ context.Context, name string) (*model.KnowledgeBase, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnGet != nil {
		return nil, f.ErrOnGet
	}
	for _, kb := range f.kbs {
		if kb.Name == name {
			cp := *kb
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

func (f *FakeKBRepo) Delete(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnDelete != nil {
		return f.ErrOnDelete
	}
	delete(f.kbs, id)
	delete(f.members, id)
	delete(f.docCount, id)
	return nil
}

func (f *FakeKBRepo) CountDocuments(_ context.Context, kbID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnDocCount != nil {
		return 0, f.ErrOnDocCount
	}
	return f.docCount[kbID], nil
}

func (f *FakeKBRepo) ListForUser(_ context.Context, userID int64) ([]repo.KBRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnList != nil {
		return nil, f.ErrOnList
	}
	var rows []repo.KBRow
	// FR-17：成员库 ∪ 全员可见库（非成员 role=0，service 层映射 my_role=org）
	for _, kb := range f.kbs {
		role, isMember := f.members[kb.ID][userID]
		if !isMember && kb.Visibility != model.KBVisibilityOrg {
			continue
		}
		if !isMember {
			role = 0
		}
		rows = append(rows, f.row(kb, role))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *FakeKBRepo) ListAll(_ context.Context) ([]repo.KBRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnList != nil {
		return nil, f.ErrOnList
	}
	rows := make([]repo.KBRow, 0, len(f.kbs))
	for _, kb := range f.kbs {
		rows = append(rows, f.row(kb, 0))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

// row 需在持锁下调用。
func (f *FakeKBRepo) row(kb *model.KnowledgeBase, role int16) repo.KBRow {
	return repo.KBRow{
		ID:          kb.ID,
		Name:        kb.Name,
		Description: kb.Description,
		Visibility:  kb.Visibility,
		CreatedBy:   kb.CreatedBy,
		CreatedAt:   kb.CreatedAt,
		UpdatedAt:   kb.UpdatedAt,
		DocCount:    f.docCount[kb.ID],
		Role:        role,
	}
}

func (f *FakeKBRepo) GetMemberRole(_ context.Context, kbID, userID int64) (int16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnRole != nil {
		return 0, f.ErrOnRole
	}
	roles, ok := f.members[kbID]
	if !ok {
		return 0, repo.ErrNotFound
	}
	role, ok := roles[userID]
	if !ok {
		return 0, repo.ErrNotFound
	}
	return role, nil
}

// GetVisibility 库可见性（FR-17）：0 值视为私有（SeedKB 建的库未显式设置）。
func (f *FakeKBRepo) GetVisibility(_ context.Context, kbID int64) (int16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnGet != nil {
		return 0, f.ErrOnGet
	}
	kb, ok := f.kbs[kbID]
	if !ok {
		return 0, repo.ErrNotFound
	}
	if kb.Visibility == 0 {
		return model.KBVisibilityPrivate, nil
	}
	return kb.Visibility, nil
}

func (f *FakeKBRepo) ListMembers(_ context.Context, kbID int64) ([]repo.KBMemberRow, error) {
	f.mu.Lock()
	if f.ErrOnMembers != nil {
		f.mu.Unlock()
		return nil, f.ErrOnMembers
	}
	roles := f.members[kbID]
	if roles == nil {
		f.mu.Unlock()
		return []repo.KBMemberRow{}, nil
	}
	type pair struct {
		uid  int64
		role int16
	}
	var list []pair
	for uid, role := range roles {
		list = append(list, pair{uid, role})
	}
	f.mu.Unlock()

	sort.Slice(list, func(i, j int) bool {
		if list[i].role != list[j].role {
			return list[i].role < list[j].role
		}
		return list[i].uid < list[j].uid
	})
	rows := make([]repo.KBMemberRow, 0, len(list))
	for _, p := range list {
		username := ""
		if f.Users != nil {
			if u, err := f.Users.GetByID(context.Background(), p.uid); err == nil {
				username = u.Username
			}
		}
		rows = append(rows, repo.KBMemberRow{UserID: p.uid, Username: username, Role: p.role, CreatedAt: time.Now()})
	}
	return rows, nil
}

func (f *FakeKBRepo) UpsertMember(_ context.Context, m *model.KBMember) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnMembers != nil {
		return f.ErrOnMembers
	}
	if f.members[m.KBID] == nil {
		f.members[m.KBID] = map[int64]int16{}
	}
	f.members[m.KBID][m.UserID] = m.Role
	return nil
}

func (f *FakeKBRepo) DeleteMember(_ context.Context, kbID, userID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnMembers != nil {
		return f.ErrOnMembers
	}
	if roles, ok := f.members[kbID]; ok {
		delete(roles, userID)
	}
	return nil
}

func (f *FakeKBRepo) UserExists(ctx context.Context, userID int64) (bool, error) {
	if f.ErrOnUserExists != nil {
		return false, f.ErrOnUserExists
	}
	if f.Users == nil {
		return true, nil
	}
	_, err := f.Users.GetByID(ctx, userID)
	if err != nil {
		return false, nil //nolint:nilerr // 用户不存在属正常路径（非错误）
	}
	return true, nil
}

// ---- 测试辅助 ----

// SeedKB 直接写入一个知识库（绕过 service）。
func (f *FakeKBRepo) SeedKB(t *testing.T, name string, createdBy int64) *model.KnowledgeBase {
	t.Helper()
	kb := &model.KnowledgeBase{Name: name, CreatedBy: createdBy}
	if err := f.Create(context.Background(), kb); err != nil {
		t.Fatalf("seed kb: %v", err)
	}
	return kb
}

// SetRole 直接设置成员角色（绕过 service）。
func (f *FakeKBRepo) SetRole(kbID, userID int64, role int16) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.members[kbID] == nil {
		f.members[kbID] = map[int64]int16{}
	}
	f.members[kbID][userID] = role
}

// SetDocCount 设置文档数（列表断言用）。
func (f *FakeKBRepo) SetDocCount(kbID, n int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docCount[kbID] = n
}

// MemberCount 成员数量（断言用）。
func (f *FakeKBRepo) MemberCount(kbID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.members[kbID])
}
