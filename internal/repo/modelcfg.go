package repo

import (
	"context"
	"time"

	"gorm.io/gorm"

	"woodpecker/internal/model"
)

// ModelServiceRow 服务列表行（含条目数）。
type ModelServiceRow struct {
	ID         int64
	Name       string
	BaseURL    string
	Enabled    bool
	EntryCount int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ModelEntryRow 条目行（含服务信息）。
type ModelEntryRow struct {
	ID          int64
	ServiceID   int64
	ServiceName string
	Purpose     int16
	ModelName   string
	Params      []byte
	IsDefault   bool
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DefaultModelConfig 默认模型配置快照（供 Provider 工厂使用；APIKey 为解密后的明文）。
type DefaultModelConfig struct {
	EntryID   int64
	ServiceID int64
	BaseURL   string
	APIKey    string
	ModelName string
	Params    string
}

type ModelCfgRepo struct{ db *gorm.DB }

func NewModelCfgRepo(db *gorm.DB) *ModelCfgRepo { return &ModelCfgRepo{db: db} }

func (r *ModelCfgRepo) dbx(ctx context.Context) *gorm.DB { return r.db.WithContext(ctx) }

// CreateService 创建服务；api_key_enc 已由上层加密。
func (r *ModelCfgRepo) CreateService(ctx context.Context, s *model.ModelService) error {
	return r.dbx(ctx).Create(s).Error
}

func (r *ModelCfgRepo) SaveService(ctx context.Context, s *model.ModelService) error {
	return r.dbx(ctx).Save(s).Error
}

func (r *ModelCfgRepo) GetServiceByID(ctx context.Context, id int64) (*model.ModelService, error) {
	var s model.ModelService
	if err := r.dbx(ctx).First(&s, id).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

func (r *ModelCfgRepo) ListServices(ctx context.Context) ([]ModelServiceRow, error) {
	const q = `
SELECT s.id, s.name, s.base_url, s.enabled, s.created_at, s.updated_at,
       (SELECT COUNT(*) FROM model_entries e WHERE e.service_id = s.id) AS entry_count
FROM model_services s
ORDER BY s.id ASC`
	var rows []ModelServiceRow
	err := r.dbx(ctx).Raw(q).Scan(&rows).Error
	return rows, err
}

func (r *ModelCfgRepo) DeleteService(ctx context.Context, id int64) error {
	return r.dbx(ctx).Where("id = ?", id).Delete(&model.ModelService{}).Error
}

func (r *ModelCfgRepo) CountEntriesByService(ctx context.Context, serviceID int64) (int64, error) {
	var n int64
	err := r.dbx(ctx).Model(&model.ModelEntry{}).Where("service_id = ?", serviceID).Count(&n).Error
	return n, err
}

func (r *ModelCfgRepo) CreateEntry(ctx context.Context, e *model.ModelEntry) error {
	return r.dbx(ctx).Create(e).Error
}

func (r *ModelCfgRepo) SaveEntry(ctx context.Context, e *model.ModelEntry) error {
	return r.dbx(ctx).Save(e).Error
}

func (r *ModelCfgRepo) GetEntryByID(ctx context.Context, id int64) (*model.ModelEntry, error) {
	var e model.ModelEntry
	if err := r.dbx(ctx).First(&e, id).Error; err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func (r *ModelCfgRepo) ListEntries(ctx context.Context, serviceID int64, purpose int16) ([]ModelEntryRow, error) {
	const q = `
SELECT e.id, e.service_id, s.name AS service_name, e.purpose, e.model_name, e.params,
       e.is_default, s.enabled, e.created_at, e.updated_at
FROM model_entries e
JOIN model_services s ON s.id = e.service_id
WHERE (? = 0 OR e.service_id = ?) AND (? = 0 OR e.purpose = ?)
ORDER BY e.purpose ASC, e.id ASC`
	var rows []ModelEntryRow
	err := r.dbx(ctx).Raw(q, serviceID, serviceID, purpose, purpose).Scan(&rows).Error
	return rows, err
}

func (r *ModelCfgRepo) DeleteEntry(ctx context.Context, id int64) error {
	return r.dbx(ctx).Where("id = ?", id).Delete(&model.ModelEntry{}).Error
}

// GetDefaultEntry 指定用途的默认条目（仅启用中的服务）；无则 ErrNotFound。
func (r *ModelCfgRepo) GetDefaultEntry(ctx context.Context, purpose int16) (*model.ModelEntry, *model.ModelService, error) {
	var e model.ModelEntry
	err := r.dbx(ctx).
		Joins("JOIN model_services s ON s.id = model_entries.service_id AND s.enabled = TRUE").
		Where("model_entries.purpose = ? AND model_entries.is_default = TRUE", purpose).
		First(&e).Error
	if err != nil {
		if isNoRows(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	var s model.ModelService
	if err := r.dbx(ctx).First(&s, e.ServiceID).Error; err != nil {
		return nil, nil, err
	}
	return &e, &s, nil
}

// SetDefaultEntry 事务内切换默认：先清旧默认再设新默认（部分唯一索引保证每用途唯一）。
func (r *ModelCfgRepo) SetDefaultEntry(ctx context.Context, purpose int16, entryID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ModelEntry{}).
			Where("purpose = ? AND is_default = TRUE", purpose).
			Update("is_default", false).Error; err != nil {
			return err
		}
		return tx.Model(&model.ModelEntry{}).
			Where("id = ?", entryID).
			Update("is_default", true).Error
	})
}
