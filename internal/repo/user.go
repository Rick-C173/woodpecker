// Package repo GORM / 手写 SQL 实现，接口定义见 internal/service（防倒置）。
package repo

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"woodpecker/internal/model"
)

// ErrNotFound 查询无结果；service 层据此映射业务错误码。
var ErrNotFound = errors.New("记录不存在")

func isNoRows(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }

type UserRepo struct{ db *gorm.DB }

func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

func (r *UserRepo) dbx(ctx context.Context) *gorm.DB { return r.db.WithContext(ctx) }

func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.dbx(ctx).Model(&model.User{}).Count(&n).Error
	return n, err
}

// CountAdmins 「可用管理员」计数：只数 is_admin=true 且 status=1（启用）。
// 禁用会保留 is_admin=true（数据保留），故必须过滤 status，否则「最后一个管理员」保护
// 会把已禁用管理员也算作有效管理员，导致系统可用管理员降到 0、后台永久锁死（P0-3）。
func (r *UserRepo) CountAdmins(ctx context.Context) (int64, error) {
	var n int64
	err := r.dbx(ctx).Model(&model.User{}).
		Where("is_admin = ? AND status = ?", true, model.UserStatusActive).
		Count(&n).Error
	return n, err
}

func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	var u model.User
	if err := r.dbx(ctx).Where("username = ?", username).First(&u).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// GetByEmail 按邮箱取用户（FR-16 登录支持邮箱）；不存在返回 ErrNotFound。
func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	var u model.User
	if err := r.dbx(ctx).Where("email = ?", email).First(&u).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

// CountByEmail 邮箱是否已被占用（FR-16 邀请查重）。
func (r *UserRepo) CountByEmail(ctx context.Context, email string) (int64, error) {
	var n int64
	err := r.dbx(ctx).Model(&model.User{}).Where("email = ?", email).
		Count(&n).Error
	return n, err
}

func (r *UserRepo) GetByID(ctx context.Context, id int64) (*model.User, error) {
	var u model.User
	if err := r.dbx(ctx).First(&u, id).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

type UserListQuery struct {
	Keyword string
	Status  int16 // 0=不过滤
	Page    int
	PerPage int
}

func (r *UserRepo) List(ctx context.Context, q UserListQuery) ([]model.User, int64, error) {
	tx := r.dbx(ctx).Model(&model.User{})
	if q.Keyword != "" {
		tx = tx.Where("username ILIKE ?", "%"+q.Keyword+"%")
	}
	if q.Status != 0 {
		tx = tx.Where("status = ?", q.Status)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []model.User
	err := tx.Order("id ASC").
		Limit(q.PerPage).Offset((q.Page - 1) * q.PerPage).
		Find(&users).Error
	return users, total, err
}

func (r *UserRepo) Create(ctx context.Context, u *model.User) error {
	return r.dbx(ctx).Create(u).Error
}

func (r *UserRepo) Save(ctx context.Context, u *model.User) error {
	return r.dbx(ctx).Save(u).Error
}

// CountKBMemberships 用户可访问的知识库数（FR-13 用户列表展示）。
func (r *UserRepo) CountKBMemberships(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := r.dbx(ctx).Table("kb_members").Where("user_id = ?", userID).Count(&n).Error
	return n, err
}
