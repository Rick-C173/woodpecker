package testutil

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// ---- 模型配置存储替身 ----

type FakeModelCfgRepo struct {
	mu       sync.Mutex
	services map[int64]*model.ModelService
	entries  map[int64]*model.ModelEntry
	nextSvc  int64
	nextEnt  int64

	ErrOnServiceCreate error
	ErrOnServiceSave   error
	ErrOnServiceGet    error
	ErrOnServiceDelete error
	ErrOnEntryCreate   error
	ErrOnEntrySave     error
	ErrOnEntryGet      error
	ErrOnEntryDelete   error
	ErrOnList          error
	ErrOnDefault       error

	GetDefaultCalls int // GetDefaultEntry 调用计数（缓存命中验证）
}

func NewFakeModelCfgRepo() *FakeModelCfgRepo {
	return &FakeModelCfgRepo{
		services: map[int64]*model.ModelService{},
		entries:  map[int64]*model.ModelEntry{},
		nextSvc:  1,
		nextEnt:  1,
	}
}

func (f *FakeModelCfgRepo) CreateService(_ context.Context, s *model.ModelService) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnServiceCreate != nil {
		return f.ErrOnServiceCreate
	}
	s.ID = f.nextSvc
	f.nextSvc++
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
		s.UpdatedAt = s.CreatedAt
	}
	cp := *s
	f.services[s.ID] = &cp
	return nil
}

func (f *FakeModelCfgRepo) SaveService(_ context.Context, s *model.ModelService) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnServiceSave != nil {
		return f.ErrOnServiceSave
	}
	if _, ok := f.services[s.ID]; !ok {
		return repo.ErrNotFound
	}
	cp := *s
	f.services[s.ID] = &cp
	return nil
}

func (f *FakeModelCfgRepo) GetServiceByID(_ context.Context, id int64) (*model.ModelService, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnServiceGet != nil {
		return nil, f.ErrOnServiceGet
	}
	s, ok := f.services[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *FakeModelCfgRepo) ListServices(_ context.Context) ([]repo.ModelServiceRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnList != nil {
		return nil, f.ErrOnList
	}
	rows := make([]repo.ModelServiceRow, 0, len(f.services))
	for _, s := range f.services {
		count := int64(0)
		for _, e := range f.entries {
			if e.ServiceID == s.ID {
				count++
			}
		}
		rows = append(rows, repo.ModelServiceRow{
			ID: s.ID, Name: s.Name, BaseURL: s.BaseURL, Enabled: s.Enabled,
			EntryCount: count, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}

func (f *FakeModelCfgRepo) DeleteService(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnServiceDelete != nil {
		return f.ErrOnServiceDelete
	}
	delete(f.services, id)
	return nil
}

func (f *FakeModelCfgRepo) CountEntriesByService(_ context.Context, serviceID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, e := range f.entries {
		if e.ServiceID == serviceID {
			n++
		}
	}
	return n, nil
}

// CountDefaultEntries 该服务下 is_default=true 的条目数（与真实现同口径，审计 P1-6）。
func (f *FakeModelCfgRepo) CountDefaultEntries(_ context.Context, serviceID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, e := range f.entries {
		if e.ServiceID == serviceID && e.IsDefault {
			n++
		}
	}
	return n, nil
}

func (f *FakeModelCfgRepo) CreateEntry(_ context.Context, e *model.ModelEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnEntryCreate != nil {
		return f.ErrOnEntryCreate
	}
	if _, ok := f.services[e.ServiceID]; !ok {
		return errors.New("service missing")
	}
	e.ID = f.nextEnt
	f.nextEnt++
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
		e.UpdatedAt = e.CreatedAt
	}
	cp := *e
	f.entries[e.ID] = &cp
	return nil
}

func (f *FakeModelCfgRepo) SaveEntry(_ context.Context, e *model.ModelEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnEntrySave != nil {
		return f.ErrOnEntrySave
	}
	if _, ok := f.entries[e.ID]; !ok {
		return repo.ErrNotFound
	}
	cp := *e
	f.entries[e.ID] = &cp
	return nil
}

func (f *FakeModelCfgRepo) GetEntryByID(_ context.Context, id int64) (*model.ModelEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnEntryGet != nil {
		return nil, f.ErrOnEntryGet
	}
	e, ok := f.entries[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *e
	return &cp, nil
}

func (f *FakeModelCfgRepo) ListEntries(_ context.Context, serviceID int64, purpose int16) ([]repo.ModelEntryRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnList != nil {
		return nil, f.ErrOnList
	}
	rows := make([]repo.ModelEntryRow, 0)
	for _, e := range f.entries {
		if serviceID != 0 && e.ServiceID != serviceID {
			continue
		}
		if purpose != 0 && e.Purpose != purpose {
			continue
		}
		svc := f.services[e.ServiceID]
		svcName, enabled := "", false
		if svc != nil {
			svcName, enabled = svc.Name, svc.Enabled
		}
		rows = append(rows, repo.ModelEntryRow{
			ID: e.ID, ServiceID: e.ServiceID, ServiceName: svcName,
			Purpose: e.Purpose, ModelName: e.ModelName, Params: e.Params,
			IsDefault: e.IsDefault, Enabled: enabled,
			CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Purpose != rows[j].Purpose {
			return rows[i].Purpose < rows[j].Purpose
		}
		return rows[i].ID < rows[j].ID
	})
	return rows, nil
}

func (f *FakeModelCfgRepo) DeleteEntry(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnEntryDelete != nil {
		return f.ErrOnEntryDelete
	}
	delete(f.entries, id)
	return nil
}

func (f *FakeModelCfgRepo) GetDefaultEntry(_ context.Context, purpose int16) (*model.ModelEntry, *model.ModelService, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.GetDefaultCalls++
	if f.ErrOnDefault != nil {
		return nil, nil, f.ErrOnDefault
	}
	for _, e := range f.entries {
		if e.Purpose == purpose && e.IsDefault {
			svc := f.services[e.ServiceID]
			if svc == nil || !svc.Enabled {
				continue // 停用服务不作为默认（与 SQL 行为一致）
			}
			cpE := *e
			cpS := *svc
			return &cpE, &cpS, nil
		}
	}
	return nil, nil, repo.ErrNotFound
}

func (f *FakeModelCfgRepo) SetDefaultEntry(_ context.Context, purpose int16, entryID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[entryID]
	if !ok {
		return repo.ErrNotFound
	}
	for _, other := range f.entries {
		if other.Purpose == purpose {
			other.IsDefault = false
		}
	}
	e.IsDefault = true
	return nil
}

// ---- 测试辅助 ----

// SeedModelService 直接写入服务（绕过 service），返回 ID。
func (f *FakeModelCfgRepo) SeedModelService(name, baseURL string, apiKey string, enabled bool) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextSvc
	f.nextSvc++
	f.services[id] = &model.ModelService{
		ID: id, Name: name, BaseURL: baseURL,
		APIKeyEnc: []byte("enc:" + apiKey), Enabled: enabled,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	return id
}

// SeedModelEntry 直接写入条目，返回 ID。
func (f *FakeModelCfgRepo) SeedModelEntry(serviceID int64, purpose int16, modelName string, isDefault bool) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextEnt
	f.nextEnt++
	f.entries[id] = &model.ModelEntry{
		ID: id, ServiceID: serviceID, Purpose: purpose, ModelName: modelName,
		Params: []byte("{}"), IsDefault: isDefault,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	return id
}

// ---- 加密替身 ----

// FakeCipher 简单前缀加解密（服务层测试用）。
type FakeCipher struct {
	FailOnEncrypt bool
	FailOnDecrypt bool
}

func (c *FakeCipher) Encrypt(plaintext []byte) ([]byte, error) {
	if c.FailOnEncrypt {
		return nil, errors.New("encrypt fail")
	}
	return []byte("enc:" + string(plaintext)), nil
}

func (c *FakeCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if c.FailOnDecrypt {
		return nil, errors.New("decrypt fail")
	}
	s := string(ciphertext)
	if !strings.HasPrefix(s, "enc:") {
		return nil, errors.New("not encrypted")
	}
	return []byte(strings.TrimPrefix(s, "enc:")), nil
}

// ---- 缓存替身 ----

type FakeCache struct {
	mu      sync.Mutex
	vals    map[string]string
	DelKeys []string
}

func NewFakeCache() *FakeCache { return &FakeCache{vals: map[string]string{}} }

func (c *FakeCache) Get(_ context.Context, key string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.vals[key]
	return v, ok, nil
}

func (c *FakeCache) Set(_ context.Context, key, val string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vals[key] = val
	return nil
}

func (c *FakeCache) Del(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.vals, key)
	c.DelKeys = append(c.DelKeys, key)
	return nil
}

func (c *FakeCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.vals)
}

// ---- 连通性测试替身 ----

type FakeTester struct {
	ChatErr   error
	EmbedErr  error
	RerankErr error
	Calls     []string
}

func (t *FakeTester) TestChat(_ context.Context, _ llm.ResolvedConfig) error {
	t.Calls = append(t.Calls, "chat")
	return t.ChatErr
}

func (t *FakeTester) TestEmbedding(_ context.Context, _ llm.ResolvedConfig) error {
	t.Calls = append(t.Calls, "embedding")
	return t.EmbedErr
}

func (t *FakeTester) TestRerank(_ context.Context, _ llm.ResolvedConfig) error {
	t.Calls = append(t.Calls, "rerank")
	return t.RerankErr
}
