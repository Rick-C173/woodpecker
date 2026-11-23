package model

import "time"

// 模型条目用途（PRD FR-12）。
const (
	ModelPurposeChat      int16 = 1
	ModelPurposeEmbedding int16 = 2
	ModelPurposeRerank    int16 = 3
)

// ModelPurposeName 用途对外名称。
func ModelPurposeName(p int16) string {
	switch p {
	case ModelPurposeChat:
		return "chat"
	case ModelPurposeEmbedding:
		return "embedding"
	case ModelPurposeRerank:
		return "rerank"
	default:
		return ""
	}
}

// ParseModelPurpose 解析 API 传入的用途字符串。
func ParseModelPurpose(s string) (int16, bool) {
	switch s {
	case "chat":
		return ModelPurposeChat, true
	case "embedding":
		return ModelPurposeEmbedding, true
	case "rerank":
		return ModelPurposeRerank, true
	default:
		return 0, false
	}
}

type ModelService struct {
	ID        int64     `gorm:"primaryKey;column:id"`
	Name      string    `gorm:"column:name;size:100"`
	BaseURL   string    `gorm:"column:base_url;size:500"`
	APIKeyEnc []byte    `gorm:"column:api_key_enc"`
	Enabled   bool      `gorm:"column:enabled"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (ModelService) TableName() string { return "model_services" }

type ModelEntry struct {
	ID        int64     `gorm:"primaryKey;column:id"`
	ServiceID int64     `gorm:"column:service_id"`
	Purpose   int16     `gorm:"column:purpose"`
	ModelName string    `gorm:"column:model_name;size:200"`
	Params    []byte    `gorm:"column:params;type:jsonb"`
	IsDefault bool      `gorm:"column:is_default"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (ModelEntry) TableName() string { return "model_entries" }
