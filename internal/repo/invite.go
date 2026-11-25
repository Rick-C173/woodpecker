package repo

import (
	"context"
	"time"

	"gorm.io/gorm"

	"woodpecker/internal/model"
)

// InviteRepo 邀请存储（FR-16）：每邮箱一条，重复邀请覆盖 token。
type InviteRepo struct{ db *gorm.DB }

func NewInviteRepo(db *gorm.DB) *InviteRepo { return &InviteRepo{db: db} }

func (r *InviteRepo) dbx(ctx context.Context) *gorm.DB { return r.db.WithContext(ctx) }

// GetByEmail 按邮箱取邀请；不存在返回 ErrNotFound。
func (r *InviteRepo) GetByEmail(ctx context.Context, email string) (*model.Invite, error) {
	var m model.Invite
	if err := r.dbx(ctx).Where("email = ?", email).First(&m).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// GetByTokenHash 按 token 哈希取邀请；不存在返回 ErrNotFound。
func (r *InviteRepo) GetByTokenHash(ctx context.Context, tokenHash string) (*model.Invite, error) {
	var m model.Invite
	if err := r.dbx(ctx).Where("token_hash = ?", tokenHash).First(&m).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// GetByID 按 ID 取邀请；不存在返回 ErrNotFound。
func (r *InviteRepo) GetByID(ctx context.Context, id int64) (*model.Invite, error) {
	var m model.Invite
	if err := r.dbx(ctx).First(&m, id).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// Save 创建或覆盖（按 email 唯一冲突更新 token/状态/有效期/发送信息）。
func (r *InviteRepo) Save(ctx context.Context, m *model.Invite) error {
	return r.dbx(ctx).Save(m).Error
}

// Create 幂等插入：邮箱已存在时更新为一次全新邀请（覆盖 token/重置状态）。
func (r *InviteRepo) Upsert(ctx context.Context, m *model.Invite) error {
	return r.dbx(ctx).Exec(`
INSERT INTO invites (email, token_hash, status, expires_at, send_status, send_count, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (email) DO UPDATE SET
    token_hash = EXCLUDED.token_hash,
    status     = EXCLUDED.status,
    expires_at = EXCLUDED.expires_at,
    accepted_at = NULL,
    accepted_by = NULL,
    send_status = EXCLUDED.send_status,
    send_error  = NULL,
    updated_at  = now()
`, m.Email, m.TokenHash, m.Status, m.ExpiresAt, m.SendStatus, m.SendCount, m.CreatedBy, time.Now(), time.Now()).Error
}

// MarkSent 记录发送结果（成功/失败）。
func (r *InviteRepo) MarkSent(ctx context.Context, id int64, sendStatus int16, sendErr string) error {
	updates := map[string]any{
		"send_status": sendStatus,
		"send_count":  gorm.Expr("send_count + 1"),
		"updated_at":  time.Now(),
	}
	if sendErr == "" {
		updates["send_error"] = nil
	} else {
		updates["send_error"] = sendErr
	}
	return r.dbx(ctx).Model(&model.Invite{}).Where("id = ?", id).Updates(updates).Error
}

// MarkAccepted 标记已接受（注册成功调用）。
func (r *InviteRepo) MarkAccepted(ctx context.Context, id, userID int64) error {
	return r.dbx(ctx).Model(&model.Invite{}).Where("id = ?", id).Updates(map[string]any{
		"status":      model.InviteStatusAccepted,
		"accepted_at": time.Now(),
		"accepted_by": userID,
	}).Error
}

// Revoke 撤销（仅待接受可撤销，调用方校验）。
func (r *InviteRepo) Revoke(ctx context.Context, id int64) error {
	return r.dbx(ctx).Model(&model.Invite{}).Where("id = ?", id).
		Updates(map[string]any{"status": model.InviteStatusRevoked, "updated_at": time.Now()}).Error
}

// InviteListQuery 邀请列表查询条件。
type InviteListQuery struct {
	Status *int16
	From   *time.Time
	To     *time.Time
	Page   int
	Size   int
}

// List 分页列表（created_at 倒序）。
func (r *InviteRepo) List(ctx context.Context, q InviteListQuery) ([]model.Invite, int64, error) {
	page, size := q.Page, q.Size
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	tx := r.dbx(ctx).Model(&model.Invite{})
	if q.Status != nil {
		tx = tx.Where("status = ?", *q.Status)
	}
	if q.From != nil {
		tx = tx.Where("created_at >= ?", *q.From)
	}
	if q.To != nil {
		tx = tx.Where("created_at <= ?", *q.To)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []model.Invite
	if err := tx.Order("created_at DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
