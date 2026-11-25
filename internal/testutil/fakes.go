// Package testutil 测试替身与测试环境组装（仅测试使用，不参与生产路径）。
package testutil

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	mu               sync.Mutex
	byName           map[string]*model.User
	nextID           int64
	ErrOnList        error // 注入 List 错误
	ErrOnSave        error // 注入 Save 错误
	ErrOnCreate      error // 注入 Create 错误
	ErrOnCount       error // 注入 Count 错误
	ErrOnCountAdmins error // 注入 CountAdmins 错误
	ErrOnKBCount     error // 注入 CountKBMemberships 错误
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

// CountAdmins 「可用管理员」计数，与 repo.UserRepo 同口径（is_admin=true 且 status=1）。
// 替身必须与真实现一致，否则「禁用 × 撤权」的组合态缺陷在单测里原理上不可测（P0-3 / 审计 §3.4）。
func (f *FakeUserRepo) CountAdmins(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnCountAdmins != nil {
		return 0, f.ErrOnCountAdmins
	}
	var n int64
	for _, u := range f.byName {
		if u.IsAdmin && u.Status == model.UserStatusActive {
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

// GetByEmail 按邮箱查找（FR-16）；邮箱统一小写比较。
func (f *FakeUserRepo) GetByEmail(_ context.Context, email string) (*model.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.byName {
		if u.Email != nil && strings.ToLower(*u.Email) == strings.ToLower(email) {
			cp := *u
			return &cp, nil
		}
	}
	return nil, repo.ErrNotFound
}

// CountByEmail 邮箱占用计数（FR-16 邀请查重）。
func (f *FakeUserRepo) CountByEmail(_ context.Context, email string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, u := range f.byName {
		if u.Email != nil && strings.ToLower(*u.Email) == strings.ToLower(email) {
			n++
		}
	}
	return n, nil
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
	Vals    map[string]bool
	StrVals map[string]string // Set 写入的字符串值（key → 原始 JSON）
	Err     error
}

func (f *FakeSettingRepo) GetBool(context.Context, string, bool) (bool, error) {
	if f.Err != nil {
		return false, f.Err
	}
	return f.Vals["self_register_enabled"], nil
}

// GetString 字符串设置（M4：exclusion_rules 用；返回 def，可按需扩展）。
func (f *FakeSettingRepo) GetString(_ context.Context, key, def string) (string, error) {
	if f.Err != nil {
		return def, f.Err
	}
	if v, ok := f.StrVals[key]; ok {
		return v, nil
	}
	return def, nil
}

// Set 记录写入（M7：settings 管理用）。
func (f *FakeSettingRepo) Set(_ context.Context, key string, value []byte, _ int64) error {
	if f.Err != nil {
		return f.Err
	}
	if f.StrVals == nil {
		f.StrVals = map[string]string{}
	}
	f.StrVals[key] = string(value)
	switch key {
	case "self_register_enabled":
		var b bool
		_ = json.Unmarshal(value, &b)
		if f.Vals == nil {
			f.Vals = map[string]bool{}
		}
		f.Vals["self_register_enabled"] = b
	}
	return nil
}

// FakeQueue 摄取任务入队替身（记录 task_id）。
type FakeQueue struct {
	IDs []int64
	Err error
}

func (f *FakeQueue) EnqueueIngest(_ context.Context, taskID int64) error {
	if f.Err != nil {
		return f.Err
	}
	f.IDs = append(f.IDs, taskID)
	return nil
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

// List 审计查询（M7：筛选分页；from/to 语义为 [from, to)）。
func (f *FakeAuditRepo) List(_ context.Context, q repo.AuditQuery) ([]repo.AuditRow, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PerPage < 1 || q.PerPage > 100 {
		q.PerPage = 20
	}
	all := make([]repo.AuditRow, 0, len(f.Entries))
	for _, e := range f.Entries {
		if q.UserID > 0 && (e.UserID == nil || *e.UserID != q.UserID) {
			continue
		}
		if q.Action != "" && e.Action != q.Action {
			continue
		}
		if q.ObjectType != "" && e.ObjectType != q.ObjectType {
			continue
		}
		if q.From != nil && e.CreatedAt.Before(*q.From) {
			continue
		}
		if q.To != nil && !e.CreatedAt.Before(*q.To) {
			continue
		}
		row := repo.AuditRow{ID: e.ID, UserID: e.UserID, Action: e.Action, ObjectType: e.ObjectType,
			ObjectID: e.ObjectID, IP: e.IP, CreatedAt: e.CreatedAt}
		if len(e.Detail) > 0 {
			var d map[string]any
			_ = json.Unmarshal(e.Detail, &d)
			row.Detail = d
		}
		all = append(all, row)
	}
	// created_at DESC：倒序返回（Entries 为写入顺序）
	rev := make([]repo.AuditRow, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		rev = append(rev, all[i])
	}
	total := int64(len(rev))
	lo := (q.Page - 1) * q.PerPage
	if lo > len(rev) {
		lo = len(rev)
	}
	hi := lo + q.PerPage
	if hi > len(rev) {
		hi = len(rev)
	}
	return rev[lo:hi], total, nil
}

// FakeFeedbackRepo 反馈查询替身（M7：管理端筛选分页）。
type FakeFeedbackRepo struct {
	Rows []repo.FeedbackAdminRow
}

func (f *FakeFeedbackRepo) ListFeedbacks(_ context.Context, q repo.FeedbackQuery) ([]repo.FeedbackAdminRow, int64, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PerPage < 1 || q.PerPage > 100 {
		q.PerPage = 20
	}
	all := make([]repo.FeedbackAdminRow, 0, len(f.Rows))
	for _, r := range f.Rows {
		if q.KBID > 0 && r.KBID != q.KBID {
			continue
		}
		if q.Reason > 0 && r.Reason != q.Reason {
			continue
		}
		if q.From != nil && r.CreatedAt.Before(*q.From) {
			continue
		}
		if q.To != nil && !r.CreatedAt.Before(*q.To) {
			continue
		}
		all = append(all, r)
	}
	total := int64(len(all))
	lo := (q.Page - 1) * q.PerPage
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + q.PerPage
	if hi > len(all) {
		hi = len(all)
	}
	return all[lo:hi], total, nil
}

// ---- 环境组装 ----

type TestEnv struct {
	Users    *FakeUserRepo
	Audit    *FakeAuditRepo
	Settings *FakeSettingRepo
	KBs      *FakeKBRepo
	Issuer   *jwt.Issuer

	AuthSvc  *service.AuthService
	AdminSvc *service.UserAdminService
	KBSvc    *service.KBService
	AuditSvc *service.AuditService
	BaseTime time.Time
}

func NewTestEnv(t *testing.T, settings map[string]bool) *TestEnv {
	t.Helper()
	users := NewFakeUsers()
	audit := &FakeAuditRepo{}
	issuer := jwt.NewIssuer("test-secret", time.Hour)
	auditSvc := service.NewAuditService(audit)
	kbs := NewFakeKBs()
	kbs.Users = users
	env := &TestEnv{
		Users:    users,
		Audit:    audit,
		Settings: &FakeSettingRepo{Vals: settings},
		KBs:      kbs,
		Issuer:   issuer,
		AuditSvc: auditSvc,
		BaseTime: time.Now(),
	}
	env.AuthSvc = service.NewAuthService(users, env.Settings, auditSvc, issuer)
	env.AdminSvc = service.NewUserAdminService(users, auditSvc)
	env.KBSvc = service.NewKBService(kbs, auditSvc)
	env.AuthSvc.SetClock(func() time.Time { return env.BaseTime })
	env.AdminSvc.SetClock(func() time.Time { return env.BaseTime })
	env.KBSvc.SetClock(func() time.Time { return env.BaseTime })
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
