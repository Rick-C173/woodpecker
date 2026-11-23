// Package testutil 测试替身与测试环境组装（仅测试使用，不参与生产路径）。
package testutil

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"woodpecker/internal/crypto"
	"woodpecker/internal/jwt"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
)

// ---- 用户存储替身 ----

type FakeUserRepo struct {
	mu              sync.Mutex
	byName          map[string]*model.User
	nextID          int64
	ErrOnList       error // 注入 List 错误
	ErrOnSave       error // 注入 Save 错误
	ErrOnCreate     error // 注入 Create 错误
	ErrOnCount      error // 注入 Count 错误
	ErrOnCountAdmins error // 注入 CountAdmins 错误
	ErrOnKBCount    error // 注入 CountKBMemberships 错误
}

func NewFakeUsers() *FakeUserRepo {
	return &FakeUserRepo{byName: map[string]*model.User{}, nextID: 1}
}

func (f *FakeUserRepo) Count(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnCount != nil {
		return 0, f.ErrOnCount
	}
	return int64(len(f.byName)), nil
}

func (f *FakeUserRepo) CountAdmins(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnCountAdmins != nil {
		return 0, f.ErrOnCountAdmins
	}
	var n int64
	for _, u := range f.byName {
		if u.IsAdmin {
			n++
		}
	}
	return n, nil
}

func (f *FakeUserRepo) GetByUsername(_ context.Context, name string) (*model.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byName[name]; ok {
		cp := *u
		return &cp, nil
	}
	return nil, repo.ErrNotFound
}

func (f *FakeUserRepo) GetByID(_ context.Context, id int64) (*model.User, error) {
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

func (f *FakeUserRepo) List(_ context.Context, q repo.UserListQuery) ([]model.User, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnList != nil {
		return nil, 0, f.ErrOnList
	}
	var all []model.User
	for _, u := range f.byName {
		if q.Keyword != "" && !contains(u.Username, q.Keyword) {
			continue
		}
		if q.Status != 0 && u.Status != q.Status {
			continue
		}
		all = append(all, *u)
	}
	total := int64(len(all))
	// 简单分页
	start := (q.Page - 1) * q.PerPage
	end := start + q.PerPage
	if start > len(all) {
		start = len(all)
	}
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], total, nil
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func (f *FakeUserRepo) Create(_ context.Context, u *model.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnCreate != nil {
		return f.ErrOnCreate
	}
	if _, ok := f.byName[u.Username]; ok {
		return errors.New("duplicate")
	}
	u.ID = f.nextID
	f.nextID++
	cp := *u
	f.byName[u.Username] = &cp
	u.ID = cp.ID
	return nil
}

func (f *FakeUserRepo) Save(_ context.Context, u *model.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnSave != nil {
		return f.ErrOnSave
	}
	if _, ok := f.byName[u.Username]; !ok {
		return repo.ErrNotFound
	}
	cp := *u
	f.byName[u.Username] = &cp
	return nil
}

func (f *FakeUserRepo) CountKBMemberships(_ context.Context, userID int64) (int64, error) {
	if f.ErrOnKBCount != nil {
		return 0, f.ErrOnKBCount
	}
	return 0, nil
}

// Snapshot 按用户名取副本（断言用）。
func (f *FakeUserRepo) Snapshot(name string) *model.User {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byName[name]; ok {
		cp := *u
		return &cp
	}
	return nil
}

// Mutate 原位修改用户字段（断言前置状态用）。
func (f *FakeUserRepo) Mutate(name string, fn func(u *model.User)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.byName[name]; ok {
		fn(u)
	}
}

// ---- 设置与审计替身 ----

type FakeSettingRepo struct {
	Vals map[string]bool
	Err  error
}

func (f *FakeSettingRepo) GetBool(context.Context, string, bool) (bool, error) {
	if f.Err != nil {
		return false, f.Err
	}
	return f.Vals["self_register_enabled"], nil
}

type FakeAuditRepo struct {
	mu       sync.Mutex
	Entries  []model.AuditLog
	FailNext bool // 注入写入失败
}

func (f *FakeAuditRepo) Add(_ context.Context, a *model.AuditLog) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailNext {
		f.FailNext = false
		return errors.New("inject audit failure")
	}
	f.Entries = append(f.Entries, *a)
	return nil
}

func (f *FakeAuditRepo) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Entries)
}

// CountAction 统计指定动作的审计条数（断言用）。
func (f *FakeAuditRepo) CountAction(action string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.Entries {
		if e.Action == action {
			n++
		}
	}
	return n
}

// ---- 环境组装 ----

type TestEnv struct {
	Users    *FakeUserRepo
	Audit    *FakeAuditRepo
	Settings *FakeSettingRepo
	Issuer   *jwt.Issuer

	AuthSvc  *service.AuthService
	AdminSvc *service.UserAdminService
	BaseTime time.Time
}

func NewTestEnv(t *testing.T, settings map[string]bool) *TestEnv {
	t.Helper()
	users := NewFakeUsers()
	audit := &FakeAuditRepo{}
	issuer := jwt.NewIssuer("test-secret", time.Hour)
	auditSvc := service.NewAuditService(audit)
	env := &TestEnv{
		Users:    users,
		Audit:    audit,
		Settings: &FakeSettingRepo{Vals: settings},
		Issuer:   issuer,
		BaseTime: time.Now(),
	}
	env.AuthSvc = service.NewAuthService(users, env.Settings, auditSvc, issuer)
	env.AdminSvc = service.NewUserAdminService(users, auditSvc)
	env.AuthSvc.SetClock(func() time.Time { return env.BaseTime })
	env.AdminSvc.SetClock(func() time.Time { return env.BaseTime })
	return env
}

func (e *TestEnv) SeedUser(t *testing.T, name, pw string, isAdmin bool) *model.User {
	t.Helper()
	hash, err := crypto.HashPassword(pw)
	require.NoError(t, err)
	u := &model.User{Username: name, PasswordHash: hash, IsAdmin: isAdmin, Status: model.UserStatusActive}
	require.NoError(t, e.Users.Create(context.Background(), u))
	return e.Users.Snapshot(name)
}
