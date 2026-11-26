package repo

import (
	"context"
	"time"

	"gorm.io/gorm"

	"woodpecker/internal/model"
)

// APITokenRepo 个人 API Token 存储（M15 / FR-20）：库中仅存 SHA-256 哈希。
type APITokenRepo struct{ db *gorm.DB }

func NewAPITokenRepo(db *gorm.DB) *APITokenRepo { return &APITokenRepo{db: db} }

func (r *APITokenRepo) dbx(ctx context.Context) *gorm.DB { return r.db.WithContext(ctx) }

// Create 插入一条 token 记录。
func (r *APITokenRepo) Create(ctx context.Context, m *model.APIToken) error {
	return r.dbx(ctx).Create(m).Error
}

// CountByUser 统计用户有效（未吊销）token 数量（上限校验用）。
func (r *APITokenRepo) CountByUser(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := r.dbx(ctx).Model(&model.APIToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).Count(&n).Error
	return n, err
}

// ListByUser 用户 token 列表（created_at 倒序；含已吊销，便于展示历史）。
func (r *APITokenRepo) ListByUser(ctx context.Context, userID int64) ([]model.APIToken, error) {
	var rows []model.APIToken
	err := r.dbx(ctx).Where("user_id = ?", userID).
		Order("created_at DESC").Find(&rows).Error
	return rows, err
}

// GetByHash 按完整明文（含 wpk_ 前缀）的 SHA-256 hex 取 token；不存在返回 ErrNotFound。
func (r *APITokenRepo) GetByHash(ctx context.Context, tokenHash string) (*model.APIToken, error) {
	var m model.APIToken
	if err := r.dbx(ctx).Where("token_hash = ?", tokenHash).First(&m).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// Revoke 软吊销（仅本人 token）；未命中（他人 token 或不存在）返回 ErrNotFound。
func (r *APITokenRepo) Revoke(ctx context.Context, id, userID int64) error {
	res := r.dbx(ctx).Model(&model.APIToken{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", id, userID).
		Update("revoked_at", time.Now())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLastUsed 刷新最近使用时间（service 层节流：距上次 >1min 才调用）。
func (r *APITokenRepo) TouchLastUsed(ctx context.Context, id int64) error {
	return r.dbx(ctx).Model(&model.APIToken{}).Where("id = ?", id).
		Update("last_used_at", time.Now()).Error
}
