package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"woodpecker/internal/cache"
	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// 错误码（与 httpx/httpx errcode 对应，文档先行约定）。
const (
	CodeModelNoDefault  = "MODEL_001" // 422 该用途缺少默认条目
	CodeModelDefaultDel = "MODEL_002" // 422 默认条目不可删除
	CodeModelHasEntries = "MODEL_003" // 409 服务下存在条目
)

// 模型服务字段上限（FR-12）。
const (
	maxModelServiceName = 100
	maxModelServiceURL  = 500
	maxModelName        = 200
)

// modelCacheTTL 默认配置缓存时长；写操作即时失效。
const modelCacheTTL = 5 * time.Minute

// ModelCipher API Key 加解密接口（crypto.AESGCM 满足）。
type ModelCipher interface {
	Encrypt(plaintext []byte) ([]byte, error)
	Decrypt(ciphertext []byte) ([]byte, error)
}

// ModelCfgRepo 模型配置存储接口（实现在 internal/repo）。
type ModelCfgRepo interface {
	CreateService(ctx context.Context, s *model.ModelService) error
	SaveService(ctx context.Context, s *model.ModelService) error
	GetServiceByID(ctx context.Context, id int64) (*model.ModelService, error)
	ListServices(ctx context.Context) ([]repo.ModelServiceRow, error)
	DeleteService(ctx context.Context, id int64) error
	CountEntriesByService(ctx context.Context, serviceID int64) (int64, error)
	CreateEntry(ctx context.Context, e *model.ModelEntry) error
	SaveEntry(ctx context.Context, e *model.ModelEntry) error
	GetEntryByID(ctx context.Context, id int64) (*model.ModelEntry, error)
	ListEntries(ctx context.Context, serviceID int64, purpose int16) ([]repo.ModelEntryRow, error)
	DeleteEntry(ctx context.Context, id int64) error
	GetDefaultEntry(ctx context.Context, purpose int16) (*model.ModelEntry, *model.ModelService, error)
	SetDefaultEntry(ctx context.Context, purpose int16, entryID int64) error
}

// ModelConfigService 模型服务配置（PRD FR-12）。
type ModelConfigService struct {
	repo     ModelCfgRepo
	cipher   ModelCipher
	cache    cache.KVCache
	tester   llm.Tester
	audit    *AuditService
	now      func() time.Time
	cacheTTL time.Duration
}

func NewModelConfigService(repo ModelCfgRepo, cipher ModelCipher, kv cache.KVCache, tester llm.Tester, audit *AuditService) *ModelConfigService {
	return &ModelConfigService{repo: repo, cipher: cipher, cache: kv, tester: tester, audit: audit, now: time.Now, cacheTTL: modelCacheTTL}
}

// SetClock 注入时钟（测试用）。
func (s *ModelConfigService) SetClock(now func() time.Time) { s.now = now }

// cacheKey 各用途默认配置的缓存键。
func cacheKey(purpose int16) string { return "wp:model-default:" + fmt.Sprint(purpose) }

// invalidateDefaults 任一写操作后失效三个用途的缓存。
func (s *ModelConfigService) invalidateDefaults(ctx context.Context) {
	for _, p := range []int16{model.ModelPurposeChat, model.ModelPurposeEmbedding, model.ModelPurposeRerank} {
		_ = s.cache.Del(ctx, cacheKey(p))
	}
}

// ---- 响应结构 ----

type ModelServiceItem struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	BaseURL    string `json:"base_url"`
	Enabled    bool   `json:"enabled"`
	EntryCount int64  `json:"entry_count"`
	HasKey     bool   `json:"has_key"` // API Key 永不回显明文
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

type ModelEntryItem struct {
	ID          int64  `json:"id"`
	ServiceID   int64  `json:"service_id"`
	ServiceName string `json:"service_name"`
	Purpose     string `json:"purpose"`
	ModelName   string `json:"model_name"`
	Params      string `json:"params"`
	IsDefault   bool   `json:"is_default"`
	Enabled     bool   `json:"enabled"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func (s *ModelConfigService) toServiceItem(r repo.ModelServiceRow) ModelServiceItem {
	return ModelServiceItem{
		ID: r.ID, Name: r.Name, BaseURL: r.BaseURL, Enabled: r.Enabled,
		EntryCount: r.EntryCount, HasKey: true,
		CreatedAt: r.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt: r.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
}

func (s *ModelConfigService) toEntryItem(r repo.ModelEntryRow) ModelEntryItem {
	return ModelEntryItem{
		ID: r.ID, ServiceID: r.ServiceID, ServiceName: r.ServiceName,
		Purpose: model.ModelPurposeName(r.Purpose), ModelName: r.ModelName,
		Params:    string(r.Params),
		IsDefault: r.IsDefault, Enabled: r.Enabled,
		CreatedAt: r.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt: r.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
}

// ---- 服务 CRUD ----

// ListServices 服务列表（API Key 不回显）。
func (s *ModelConfigService) ListServices(ctx context.Context) ([]ModelServiceItem, error) {
	rows, err := s.repo.ListServices(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]ModelServiceItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.toServiceItem(r))
	}
	return items, nil
}

// CreateService 创建服务：名称/BaseURL 必填，API Key 加密落库。
func (s *ModelConfigService) CreateService(ctx context.Context, actorID int64, name, baseURL, apiKey, ip string) (*ModelServiceItem, error) {
	name, baseURL = strings.TrimSpace(name), strings.TrimSpace(baseURL)
	if name == "" || len(name) > maxModelServiceName {
		return nil, NewServiceError(CodeValidation, "服务名称不能为空且不超过 100 字符")
	}
	if err := validateBaseURL(baseURL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, NewServiceError(CodeValidation, "API Key 不能为空")
	}
	enc, err := s.cipher.Encrypt([]byte(apiKey))
	if err != nil {
		return nil, err
	}
	now := s.now()
	svc := &model.ModelService{Name: name, BaseURL: baseURL, APIKeyEnc: enc, Enabled: true, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateService(ctx, svc); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &actorID, "model.service_update", "model_service", &svc.ID,
		map[string]any{"action": "create", "name": name}, ip)
	s.invalidateDefaults(ctx)
	return &ModelServiceItem{ID: svc.ID, Name: name, BaseURL: baseURL, Enabled: true, HasKey: true}, nil
}

// UpdateService 更新/启停：各字段缺省表示不修改；API Key 缺省不修改。
func (s *ModelConfigService) UpdateService(ctx context.Context, actorID, id int64, name, baseURL, apiKey *string, enabled *bool, ip string) (*ModelServiceItem, error) {
	svc, err := s.repo.GetServiceByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "模型服务不存在")
		}
		return nil, err
	}
	detail := map[string]any{}
	if name != nil {
		n := strings.TrimSpace(*name)
		if n == "" || len(n) > maxModelServiceName {
			return nil, NewServiceError(CodeValidation, "服务名称不能为空且不超过 100 字符")
		}
		svc.Name, detail["name"] = n, n
	}
	if baseURL != nil {
		b := strings.TrimSpace(*baseURL)
		if err := validateBaseURL(b); err != nil {
			return nil, err
		}
		svc.BaseURL, detail["base_url"] = b, b
	}
	if apiKey != nil && strings.TrimSpace(*apiKey) != "" {
		enc, err := s.cipher.Encrypt([]byte(*apiKey))
		if err != nil {
			return nil, err
		}
		svc.APIKeyEnc = enc
		detail["api_key"] = "replaced"
	}
	if enabled != nil {
		svc.Enabled = *enabled
		detail["enabled"] = *enabled
	}
	svc.UpdatedAt = s.now()
	if err := s.repo.SaveService(ctx, svc); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, &actorID, "model.service_update", "model_service", &id, detail, ip)
	s.invalidateDefaults(ctx)
	return &ModelServiceItem{
		ID: svc.ID, Name: svc.Name, BaseURL: svc.BaseURL, Enabled: svc.Enabled,
		HasKey: true, UpdatedAt: svc.UpdatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}

// DeleteService 删除服务：存在条目时 409 MODEL_003。
func (s *ModelConfigService) DeleteService(ctx context.Context, actorID, id int64, ip string) error {
	svc, err := s.repo.GetServiceByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "模型服务不存在")
		}
		return err
	}
	n, err := s.repo.CountEntriesByService(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return NewServiceError(CodeModelHasEntries, "服务下存在模型条目，请先删除条目")
	}
	if err := s.repo.DeleteService(ctx, id); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "model.service_update", "model_service", &id,
		map[string]any{"action": "delete", "name": svc.Name}, ip)
	s.invalidateDefaults(ctx)
	return nil
}

// TestConnection 逐项验证该服务下已有条目的用途连通性（FR-12）。
// 返回 purpose → "ok" 或 "err: 原因"；该服务未配置的用途不出现在结果中。
func (s *ModelConfigService) TestConnection(ctx context.Context, actorID, serviceID int64, ip string) (map[string]string, error) {
	svc, err := s.repo.GetServiceByID(ctx, serviceID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "模型服务不存在")
		}
		return nil, err
	}
	rows, err := s.repo.ListEntries(ctx, serviceID, 0)
	if err != nil {
		return nil, err
	}
	key, err := s.cipher.Decrypt(svc.APIKeyEnc)
	if err != nil {
		return nil, fmt.Errorf("解密 API Key: %w", err)
	}
	result := map[string]string{}
	tested := map[int16]bool{}
	for _, r := range rows {
		if tested[r.Purpose] {
			continue // 同用途多条目只测第一条
		}
		tested[r.Purpose] = true
		cfg := llm.ResolvedConfig{BaseURL: svc.BaseURL, APIKey: string(key), ModelName: r.ModelName, Params: string(r.Params)}
		var terr error
		switch r.Purpose {
		case model.ModelPurposeChat:
			terr = s.tester.TestChat(ctx, cfg)
		case model.ModelPurposeEmbedding:
			terr = s.tester.TestEmbedding(ctx, cfg)
		case model.ModelPurposeRerank:
			terr = s.tester.TestRerank(ctx, cfg)
		}
		if terr != nil {
			result[model.ModelPurposeName(r.Purpose)] = "err: " + terr.Error()
		} else {
			result[model.ModelPurposeName(r.Purpose)] = "ok"
		}
	}
	s.audit.Record(ctx, &actorID, "model.service_update", "model_service", &serviceID,
		map[string]any{"action": "test_connection", "result": result}, ip)
	return result, nil
}

// ---- 条目 CRUD ----

// ListEntries 条目列表（可按服务/用途过滤）。
func (s *ModelConfigService) ListEntries(ctx context.Context, serviceID int64, purpose int16) ([]ModelEntryItem, error) {
	rows, err := s.repo.ListEntries(ctx, serviceID, purpose)
	if err != nil {
		return nil, err
	}
	items := make([]ModelEntryItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.toEntryItem(r))
	}
	return items, nil
}

// entryInput 条目创建/更新公共校验：用途合法、模型名非空、params 为合法 JSON 对象。
func validateEntryInput(purpose int16, modelName, params string) error {
	if purpose == 0 {
		return NewServiceError(CodeValidation, "purpose 取值仅 chat|embedding|rerank")
	}
	if strings.TrimSpace(modelName) == "" || len(modelName) > maxModelName {
		return NewServiceError(CodeValidation, "模型名不能为空且不超过 200 字符")
	}
	if strings.TrimSpace(params) == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(params), &m); err != nil {
		return NewServiceError(CodeValidation, "params 必须是合法的 JSON 对象")
	}
	return nil
}

// CreateEntry 创建条目；isDefault=true 时要求服务启用中并切换默认。
func (s *ModelConfigService) CreateEntry(ctx context.Context, actorID int64, serviceID int64, purposeStr, modelName, params string, isDefault bool, ip string) (*ModelEntryItem, error) {
	svc, err := s.repo.GetServiceByID(ctx, serviceID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "模型服务不存在")
		}
		return nil, err
	}
	purpose, ok := model.ParseModelPurpose(purposeStr)
	if !ok {
		return nil, NewServiceError(CodeValidation, "purpose 取值仅 chat|embedding|rerank")
	}
	if err := validateEntryInput(purpose, modelName, params); err != nil {
		return nil, err
	}
	if isDefault && !svc.Enabled {
		return nil, NewServiceError(CodeValidation, "已停用的服务不允许作为默认")
	}
	if strings.TrimSpace(params) == "" {
		params = "{}"
	}
	// 先落库为非默认，再经 SetDefaultEntry 清同用途旧默认（部分唯一索引保证唯一）
	now := s.now()
	e := &model.ModelEntry{ServiceID: serviceID, Purpose: purpose, ModelName: modelName, Params: []byte(params), IsDefault: false, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateEntry(ctx, e); err != nil {
		return nil, err
	}
	if isDefault {
		if err := s.repo.SetDefaultEntry(ctx, purpose, e.ID); err != nil {
			return nil, err
		}
	}
	s.audit.Record(ctx, &actorID, "model.entry_update", "model_entry", &e.ID,
		map[string]any{"action": "create", "service_id": serviceID, "purpose": purposeStr, "model": modelName, "is_default": isDefault}, ip)
	s.invalidateDefaults(ctx)
	return &ModelEntryItem{ID: e.ID, ServiceID: serviceID, ServiceName: svc.Name, Purpose: purposeStr, ModelName: modelName, Params: params, IsDefault: isDefault, Enabled: svc.Enabled}, nil
}

// UpdateEntry 更新条目：字段缺省不修改；isDefault=true 切换默认（服务须启用中）。
func (s *ModelConfigService) UpdateEntry(ctx context.Context, actorID, id int64, purposeStr, modelName, params *string, isDefault *bool, ip string) (*ModelEntryItem, error) {
	e, err := s.repo.GetEntryByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "模型条目不存在")
		}
		return nil, err
	}
	svc, err := s.repo.GetServiceByID(ctx, e.ServiceID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "模型服务不存在")
		}
		return nil, err
	}
	detail := map[string]any{}
	if purposeStr != nil {
		p, ok := model.ParseModelPurpose(*purposeStr)
		if !ok {
			return nil, NewServiceError(CodeValidation, "purpose 取值仅 chat|embedding|rerank")
		}
		e.Purpose, detail["purpose"] = p, *purposeStr
	}
	if modelName != nil {
		if err := validateEntryInput(e.Purpose, *modelName, "{}"); err != nil {
			return nil, err
		}
		e.ModelName, detail["model"] = *modelName, *modelName
	}
	if params != nil {
		if err := validateEntryInput(e.Purpose, "x", *params); err != nil {
			return nil, err
		}
		p := *params
		if strings.TrimSpace(p) == "" {
			p = "{}"
		}
		e.Params, detail["params"] = []byte(p), p
	}
	if isDefault != nil && *isDefault {
		if !svc.Enabled {
			return nil, NewServiceError(CodeValidation, "已停用的服务不允许作为默认")
		}
		if err := s.repo.SetDefaultEntry(ctx, e.Purpose, e.ID); err != nil {
			return nil, err
		}
		detail["is_default"] = true
	}
	if isDefault != nil && !*isDefault && e.IsDefault {
		return nil, NewServiceError(CodeModelDefaultDel, "默认条目不可取消默认，请先将默认切换到其他条目")
	}
	e.UpdatedAt = s.now()
	if err := s.repo.SaveEntry(ctx, e); err != nil {
		return nil, err
	}
	// isDefault 切换后重读最新状态
	if isDefault != nil {
		e, err = s.repo.GetEntryByID(ctx, id)
		if err != nil {
			return nil, err
		}
	}
	s.audit.Record(ctx, &actorID, "model.entry_update", "model_entry", &id, detail, ip)
	s.invalidateDefaults(ctx)
	return &ModelEntryItem{
		ID: e.ID, ServiceID: e.ServiceID, ServiceName: svc.Name,
		Purpose: model.ModelPurposeName(e.Purpose), ModelName: e.ModelName,
		Params: string(e.Params), IsDefault: e.IsDefault, Enabled: svc.Enabled,
		UpdatedAt: e.UpdatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}

// DeleteEntry 删除条目：默认条目拦截 MODEL_002。
func (s *ModelConfigService) DeleteEntry(ctx context.Context, actorID, id int64, ip string) error {
	e, err := s.repo.GetEntryByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return NewServiceError(CodeNotFound, "模型条目不存在")
		}
		return err
	}
	if e.IsDefault {
		return NewServiceError(CodeModelDefaultDel, "默认条目不可删除，请先将默认切换到其他条目")
	}
	if err := s.repo.DeleteEntry(ctx, id); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "model.entry_update", "model_entry", &id,
		map[string]any{"action": "delete", "purpose": model.ModelPurposeName(e.Purpose)}, ip)
	s.invalidateDefaults(ctx)
	return nil
}

// ---- 默认配置（带缓存） ----

// cachedDefault 缓存载荷：API Key 存密文（base64），命中后再解密。
type cachedDefault struct {
	EntryID   int64  `json:"entry_id"`
	ServiceID int64  `json:"service_id"`
	BaseURL   string `json:"base_url"`
	APIKeyEnc string `json:"api_key_enc"`
	ModelName string `json:"model_name"`
	Params    string `json:"params"`
}

// GetDefault 解析某用途的默认配置（Redis 缓存 + 写时失效）；无默认 → MODEL_001。
func (s *ModelConfigService) GetDefault(ctx context.Context, purpose int16) (llm.ResolvedConfig, error) {
	key := cacheKey(purpose)
	if raw, ok, _ := s.cache.Get(ctx, key); ok {
		var cd cachedDefault
		if err := json.Unmarshal([]byte(raw), &cd); err == nil {
			enc, derr := base64.StdEncoding.DecodeString(cd.APIKeyEnc)
			if derr == nil {
				if plain, perr := s.cipher.Decrypt(enc); perr == nil {
					return llm.ResolvedConfig{
						BaseURL: cd.BaseURL, APIKey: string(plain),
						ModelName: cd.ModelName, Params: cd.Params,
					}, nil
				}
			}
		}
		// 缓存损坏：静默回源
		_ = s.cache.Del(ctx, key)
	}
	e, svc, err := s.repo.GetDefaultEntry(ctx, purpose)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return llm.ResolvedConfig{}, NewServiceError(CodeModelNoDefault, fmt.Sprintf("用途 %s 缺少默认模型条目", model.ModelPurposeName(purpose)))
		}
		return llm.ResolvedConfig{}, err
	}
	plain, err := s.cipher.Decrypt(svc.APIKeyEnc)
	if err != nil {
		return llm.ResolvedConfig{}, fmt.Errorf("解密 API Key: %w", err)
	}
	cfg := llm.ResolvedConfig{BaseURL: svc.BaseURL, APIKey: string(plain), ModelName: e.ModelName, Params: string(e.Params)}
	if payload, jerr := json.Marshal(cachedDefault{
		EntryID: e.ID, ServiceID: svc.ID, BaseURL: svc.BaseURL,
		APIKeyEnc: base64.StdEncoding.EncodeToString(svc.APIKeyEnc),
		ModelName: e.ModelName, Params: string(e.Params),
	}); jerr == nil {
		_ = s.cache.Set(ctx, key, string(payload), s.cacheTTL)
	}
	return cfg, nil
}

func validateBaseURL(u string) error {
	if u == "" || len(u) > maxModelServiceURL {
		return NewServiceError(CodeValidation, "Base URL 不能为空且不超过 500 字符")
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return NewServiceError(CodeValidation, "Base URL 必须以 http:// 或 https:// 开头")
	}
	return nil
}
