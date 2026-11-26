package repo

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"woodpecker/internal/model"
)

// GroupRow 组列表行：含成员数与授权库数。
type GroupRow struct {
	ID          int64
	Name        string
	Description string
	CreatedBy   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
	MemberCount int64
	KBCount     int64
}

// GroupMemberRow 组成员行（含用户名）。
type GroupMemberRow struct {
	UserID    int64
	Username  string
	CreatedAt time.Time
}

// KBGroupRoleRow 库的组授权行（含组名）。
type KBGroupRoleRow struct {
	GroupID   int64
	Name      string
	Role      int16
	CreatedAt time.Time
}

type GroupRepo struct{ db *gorm.DB }

func NewGroupRepo(db *gorm.DB) *GroupRepo { return &GroupRepo{db: db} }

func (r *GroupRepo) dbx(ctx context.Context) *gorm.DB { return r.db.WithContext(ctx) }

// Create 建组（name 唯一约束由调用方预查 + DB 兜底；Auto 标识 OIDC 自动组）。
func (r *GroupRepo) Create(ctx context.Context, g *model.UserGroup) error {
	return r.dbx(ctx).Create(g).Error
}

// Save 保存组（改名/描述）。
func (r *GroupRepo) Save(ctx context.Context, g *model.UserGroup) error {
	return r.dbx(ctx).Save(g).Error
}

func (r *GroupRepo) GetByID(ctx context.Context, id int64) (*model.UserGroup, error) {
	var g model.UserGroup
	if err := r.dbx(ctx).First(&g, id).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &g, nil
}

func (r *GroupRepo) GetByName(ctx context.Context, name string) (*model.UserGroup, error) {
	var g model.UserGroup
	if err := r.dbx(ctx).Where("name = ?", name).First(&g).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &g, nil
}

// Delete 删组（级联解除组员与授权由 DB ON DELETE CASCADE 完成）。
func (r *GroupRepo) Delete(ctx context.Context, id int64) error {
	return r.dbx(ctx).Delete(&model.UserGroup{}, id).Error
}

// List 组列表（含成员数与授权库数）。
func (r *GroupRepo) List(ctx context.Context) ([]GroupRow, error) {
	const q = `
SELECT g.id, g.name, COALESCE(g.description, '') AS description, g.created_by, g.created_at, g.updated_at,
       (SELECT COUNT(*) FROM group_members m WHERE m.group_id = g.id) AS member_count,
       (SELECT COUNT(*) FROM kb_group_roles r WHERE r.group_id = g.id) AS kb_count
FROM user_groups g
ORDER BY g.id DESC`
	var rows []GroupRow
	err := r.dbx(ctx).Raw(q).Scan(&rows).Error
	return rows, err
}

// AddMember 加组员（UNIQUE 冲突交由调用方判 409）。
func (r *GroupRepo) AddMember(ctx context.Context, groupID, userID int64) error {
	m := model.GroupMember{GroupID: groupID, UserID: userID}
	return r.dbx(ctx).Create(&m).Error
}

// RemoveMember 移出组。
func (r *GroupRepo) RemoveMember(ctx context.Context, groupID, userID int64) error {
	res := r.dbx(ctx).Where("group_id = ? AND user_id = ?", groupID, userID).Delete(&model.GroupMember{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListMembers 组成员列表（含用户名）。
func (r *GroupRepo) ListMembers(ctx context.Context, groupID int64) ([]GroupMemberRow, error) {
	const q = `
SELECT m.user_id, u.username, m.created_at
FROM group_members m
JOIN users u ON u.id = m.user_id
WHERE m.group_id = ?
ORDER BY m.created_at DESC, m.user_id DESC`
	var rows []GroupMemberRow
	err := r.dbx(ctx).Raw(q, groupID).Scan(&rows).Error
	return rows, err
}

// GetMemberByID 查用户是否在组内。
func (r *GroupRepo) GetMemberByID(ctx context.Context, groupID, userID int64) (*model.GroupMember, error) {
	var m model.GroupMember
	err := r.dbx(ctx).Where("group_id = ? AND user_id = ?", groupID, userID).First(&m).Error
	if err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// ListAutoGroupsByUser 用户当前所在且参与 OIDC 同步的组（FR-19 组同步对齐用）。
func (r *GroupRepo) ListAutoGroupsByUser(ctx context.Context, userID int64) ([]model.UserGroup, error) {
	var rows []model.UserGroup
	err := r.dbx(ctx).
		Joins("JOIN group_members m ON m.group_id = user_groups.id AND m.user_id = ?", userID).
		Where("user_groups.auto = ?", true).
		Find(&rows).Error
	return rows, err
}

// Grant 组授权到库（UNIQUE 冲突交由调用方判 409；Upsert 覆盖由调用方决定）。
func (r *GroupRepo) Grant(ctx context.Context, g *model.KBGroupRole) error {
	return r.dbx(ctx).Create(g).Error
}

// UpdateGrant 变更组角色（不存在 → ErrNotFound）。
func (r *GroupRepo) UpdateGrant(ctx context.Context, kbID, groupID int64, role int16) error {
	res := r.dbx(ctx).Model(&model.KBGroupRole{}).
		Where("kb_id = ? AND group_id = ?", kbID, groupID).
		Update("role", role)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeGrant 撤销组授权。
func (r *GroupRepo) RevokeGrant(ctx context.Context, kbID, groupID int64) error {
	res := r.dbx(ctx).Where("kb_id = ? AND group_id = ?", kbID, groupID).Delete(&model.KBGroupRole{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListGrantsByKB 库的组授权列表（含组名）。
func (r *GroupRepo) ListGrantsByKB(ctx context.Context, kbID int64) ([]KBGroupRoleRow, error) {
	const q = `
SELECT r.group_id, g.name, r.role, r.created_at
FROM kb_group_roles r
JOIN user_groups g ON g.id = r.group_id
WHERE r.kb_id = ?
ORDER BY r.created_at DESC, r.group_id DESC`
	var rows []KBGroupRoleRow
	err := r.dbx(ctx).Raw(q, kbID).Scan(&rows).Error
	return rows, err
}

// ListGrantsByGroup 组授权到哪些库（组详情/删组提示用）。
func (r *GroupRepo) ListGrantsByGroup(ctx context.Context, groupID int64) ([]model.KBGroupRole, error) {
	var rows []model.KBGroupRole
	err := r.dbx(ctx).Where("group_id = ?", groupID).Order("id DESC").Find(&rows).Error
	return rows, err
}

// GetEffectiveRole 库内有效角色（FR-18）：
// 直接授权(kb_members) 与组授权(kb_group_roles ∩ group_members) 的最高者（数值越小权限越高）。
// 无任何角色 → ErrNotFound。每次实时查询（组员/授权变化即时生效）。
func (r *GroupRepo) GetEffectiveRole(ctx context.Context, kbID, userID int64) (int16, error) {
	const q = `
SELECT LEAST(
  COALESCE((SELECT role FROM kb_members WHERE kb_id = ? AND user_id = ?), 99),
  COALESCE((SELECT MIN(r.role) FROM kb_group_roles r
            JOIN group_members m ON m.group_id = r.group_id
            WHERE r.kb_id = ? AND m.user_id = ?), 99)
) AS effective`
	var eff int16
	if err := r.dbx(ctx).Raw(q, kbID, userID, kbID, userID).Scan(&eff).Error; err != nil {
		return 0, err
	}
	if eff == 0 || eff >= 99 {
		return 0, ErrNotFound
	}
	return eff, nil
}

// GrantByGroupAndKB 定位单条授权（判 409 已授权 / PATCH 前置存在性）。
func (r *GroupRepo) GrantByGroupAndKB(ctx context.Context, kbID, groupID int64) (*model.KBGroupRole, error) {
	var g model.KBGroupRole
	err := r.dbx(ctx).Where("kb_id = ? AND group_id = ?", kbID, groupID).First(&g).Error
	if err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &g, nil
}

// 编译期断言：防误用 Upsert 语义。
var _ = clause.OnConflict{}
