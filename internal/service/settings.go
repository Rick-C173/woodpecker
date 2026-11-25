package service

import (
	"context"
	"encoding/json"
	"strings"

	"woodpecker/internal/repo"
)

// 系统设置键（数据库设计 §3.6）。
const (
	SettingExclusionRules = "exclusion_rules"       // {"patterns":[...]}，R1b 敏感文件排除
	SettingSelfRegister   = "self_register_enabled" // 自助注册开关（FR-01）
)

// SettingStore 设置存储（repo.SettingRepo 满足）。
type SettingStore interface {
	GetBool(ctx context.Context, key string, def bool) (bool, error)
	GetString(ctx context.Context, key, def string) (string, error)
	Set(ctx context.Context, key string, value []byte, updatedBy int64) error
}

// SettingsView 设置展示（GET /admin/settings）。
type SettingsView struct {
	// 自定义排除规则（glob 数组）；null=未自定义，用内置默认清单
	ExclusionRules []string `json:"exclusion_rules"` // null=未自定义（用默认清单）
	// 自助注册开关（POST /auth/register 是否可用，默认关闭，FR-01）
	SelfRegister bool `json:"self_register"` // 自助注册开关（默认关闭）
	// 排除规则是否已自定义
	ExclusionCustom bool `json:"exclusion_rules_customized"` // 是否已自定义
}

// SettingsService 系统设置管理（FR-13 / FR-01 / R1b）。
type SettingsService struct {
	settings SettingStore
	audit    *AuditService
}

func NewSettingsService(settings SettingStore, audit *AuditService) *SettingsService {
	return &SettingsService{settings: settings, audit: audit}
}

// Get 设置当前值（缺省值直接展示，前端一次拉全）。
func (s *SettingsService) Get(ctx context.Context) (*SettingsView, error) {
	v := &SettingsView{SelfRegister: false}
	enabled, err := s.settings.GetBool(ctx, SettingSelfRegister, false)
	if err != nil {
		return nil, err
	}
	v.SelfRegister = enabled
	raw, err := s.settings.GetString(ctx, SettingExclusionRules, "")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) != "" {
		var parsed struct {
			Patterns []string `json:"patterns"`
		}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return nil, NewServiceError(CodeValidation, "排除规则 JSON 损坏，请重新保存")
		}
		v.ExclusionRules = parsed.Patterns
		v.ExclusionCustom = true
	}
	return v, nil
}

// UpdateRequest 设置变更（PATCH /admin/settings；nil=不改）。
type UpdateRequest struct {
	ExclusionRules *[]string `json:"exclusion_rules"`
	SelfRegister   *bool     `json:"self_register"`
}

// Update 逐项写入并记审计（setting.update）。
func (s *SettingsService) Update(ctx context.Context, actorID int64, req UpdateRequest, ip string) error {
	if req.ExclusionRules != nil {
		if err := s.setExclusionRules(ctx, actorID, *req.ExclusionRules, ip); err != nil {
			return err
		}
	}
	if req.SelfRegister != nil {
		v, err := json.Marshal(*req.SelfRegister)
		if err != nil {
			return err
		}
		if err := s.settings.Set(ctx, SettingSelfRegister, v, actorID); err != nil {
			return err
		}
		s.audit.Record(ctx, &actorID, "setting.update", "setting", nil,
			map[string]any{"key": SettingSelfRegister, "value": *req.SelfRegister}, ip)
	}
	return nil
}

// setExclusionRules 校验并写入排除规则。
func (s *SettingsService) setExclusionRules(ctx context.Context, actorID int64, patterns []string, ip string) error {
	// 清洗：去空项与首尾空白；空清单=恢复默认
	cleaned := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		cleaned = DefaultExclusionPatterns()
	}
	payload, err := json.Marshal(map[string]any{"patterns": cleaned})
	if err != nil {
		return err
	}
	if !json.Valid(payload) {
		return NewServiceError(CodeValidation, "排除规则序列化失败")
	}
	if err := s.settings.Set(ctx, SettingExclusionRules, payload, actorID); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "setting.update", "setting", nil,
		map[string]any{"key": SettingExclusionRules, "patterns": cleaned}, ip)
	return nil
}

// DefaultExclusionPatterns R1b 默认排除清单（与 rag 包默认一致，便于展示）。
func DefaultExclusionPatterns() []string {
	return []string{".env*", "*.pem", "*.key", "id_rsa*", "credentials*", "*secret*", "*.p12", "secrets/"}
}

// ---- 审计与反馈查询（管理员） ----

// AdminAuditQuery 审计查询参数（handler 归一化后传入）。
type AdminAuditQuery = repo.AuditQuery

// AdminFeedbackQuery 反馈查询参数。
type AdminFeedbackQuery = repo.FeedbackQuery

// AdminQueryService 管理端查询（FR-15 / FR-14 查询侧）。
type AdminQueryService struct {
	audit    AuditRepoAlias
	feedback FeedbackLookup
}

// AuditRepoAlias 审计仓储（repo.AuditRepo 满足）。
type AuditRepoAlias interface {
	List(ctx context.Context, q AdminAuditQuery) ([]repo.AuditRow, int64, error)
}

// FeedbackLookup 反馈仓储（repo.ConversationRepo 满足）。
type FeedbackLookup interface {
	ListFeedbacks(ctx context.Context, q AdminFeedbackQuery) ([]repo.FeedbackAdminRow, int64, error)
}

func NewAdminQueryService(audit AuditRepoAlias, feedback FeedbackLookup) *AdminQueryService {
	return &AdminQueryService{audit: audit, feedback: feedback}
}

// ListAuditLogs 审计日志查询。
func (s *AdminQueryService) ListAuditLogs(ctx context.Context, q AdminAuditQuery) ([]repo.AuditRow, int64, error) {
	return s.audit.List(ctx, q)
}

// ListFeedbacks 反馈查询。
func (s *AdminQueryService) ListFeedbacks(ctx context.Context, q AdminFeedbackQuery) ([]repo.FeedbackAdminRow, int64, error) {
	return s.feedback.ListFeedbacks(ctx, q)
}
