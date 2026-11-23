// Package service 业务逻辑层：不出现 *gin.Context 与 GORM 类型，依赖以接口注入（技术选型 §8）。
package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"woodpecker/internal/model"
)

// AuditRepo 审计存储接口（实现在 internal/repo）。
type AuditRepo interface {
	Add(ctx context.Context, a *model.AuditLog) error
}

// SettingRepo 布尔设置读取接口（实现在 internal/repo）。
type SettingRepo interface {
	GetBool(ctx context.Context, key string, def bool) (bool, error)
}

// AuditService 统一审计写入器（PRD FR-15）：失败不影响业务主流程，仅记运行日志。
type AuditService struct {
	repo AuditRepo
	now  func() time.Time
}

func NewAuditService(repo AuditRepo) *AuditService {
	return &AuditService{repo: repo, now: time.Now}
}

// Record 写一条审计；detail 可为 nil。
func (s *AuditService) Record(ctx context.Context, userID *int64, action, objectType string, objectID *int64, detail map[string]any, ip string) {
	var payload []byte
	if detail != nil {
		payload, _ = json.Marshal(detail)
	}
	entry := &model.AuditLog{
		UserID:     userID,
		Action:     action,
		ObjectType: objectType,
		ObjectID:   objectID,
		Detail:     payload,
		IP:         ip,
		CreatedAt:  s.now(),
	}
	if err := s.repo.Add(ctx, entry); err != nil {
		slog.Warn("审计写入失败（不影响主流程）", "action", action, "err", err)
	}
}
