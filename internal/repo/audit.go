package repo

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"woodpecker/internal/model"
)

type AuditRepo struct{ db *gorm.DB }

func NewAuditRepo(db *gorm.DB) *AuditRepo { return &AuditRepo{db: db} }

func (r *AuditRepo) Add(ctx context.Context, a *model.AuditLog) error {
	return r.db.WithContext(ctx).Create(a).Error
}

type SettingRepo struct{ db *gorm.DB }

func NewSettingRepo(db *gorm.DB) *SettingRepo { return &SettingRepo{db: db} }

// GetBool 读取布尔设置；键不存在返回 def（如 self_register_enabled 默认 false，PRD FR-01）。
func (r *SettingRepo) GetBool(ctx context.Context, key string, def bool) (bool, error) {
	var s model.Setting
	err := r.db.WithContext(ctx).First(&s, "key = ?", key).Error
	if err != nil {
		if isNoRows(err) {
			return def, nil
		}
		return def, err
	}
	var v bool
	if err := json.Unmarshal(s.Value, &v); err != nil {
		return def, err
	}
	return v, nil
}

// GetString 读取字符串设置（如 exclusion_rules 的 JSON 文本）；键不存在返回 def。
func (r *SettingRepo) GetString(ctx context.Context, key, def string) (string, error) {
	var s model.Setting
	err := r.db.WithContext(ctx).First(&s, "key = ?", key).Error
	if err != nil {
		if isNoRows(err) {
			return def, nil
		}
		return def, err
	}
	return string(s.Value), nil
}
