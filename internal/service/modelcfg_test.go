package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/httpx"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

type mcEnv struct {
	env    *testutil.TestEnv
	repo   *testutil.FakeModelCfgRepo
	cipher *testutil.FakeCipher
	cache  *testutil.FakeCache
	tester *testutil.FakeTester
	svc    *service.ModelConfigService
}

func mcSetup(t *testing.T) *mcEnv {
	t.Helper()
	env := testutil.NewTestEnv(t, nil)
	repoF := testutil.NewFakeModelCfgRepo()
	cipher := &testutil.FakeCipher{}
	cacheF := testutil.NewFakeCache()
	tester := &testutil.FakeTester{}
	svc := service.NewModelConfigService(repoF, cipher, cacheF, tester, env.AuditSvc)
	return &mcEnv{env: env, repo: repoF, cipher: cipher, cache: cacheF, tester: tester, svc: svc}
}

func TestMCServiceCRUD(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()

	// 创建：API Key 加密落库
	item, err := e.svc.CreateService(ctx, 1, "DeepSeek", "https://api.deepseek.com/v1", "sk-secret", "ip")
	require.NoError(t, err)
	assert.True(t, item.HasKey)
	assert.NotEmpty(t, item.ID)
	stored, err := e.repo.GetServiceByID(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, "enc:sk-secret", string(stored.APIKeyEnc), "API Key 必须加密落库")
	assert.Equal(t, 1, e.env.Audit.CountAction("model.service_update"))

	// 校验：名称为空 / URL 非法 / key 为空
	_, err = e.svc.CreateService(ctx, 1, "", "https://x.com", "k", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.CreateService(ctx, 1, "x", "ftp://x.com", "k", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.CreateService(ctx, 1, "x", "https://x.com", " ", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 列表：不泄露 Key
	list, err := e.svc.ListServices(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.True(t, list[0].HasKey)
	raw, _ := jsonOf(list)
	assert.NotContains(t, raw, "sk-secret")

	// 更新：key 缺省不修改；停用
	newURL := "https://api.deepseek.com/v2"
	updated, err := e.svc.UpdateService(ctx, 1, item.ID, nil, &newURL, nil, boolPtr(false), "")
	require.NoError(t, err)
	assert.False(t, updated.Enabled)
	stored, _ = e.repo.GetServiceByID(ctx, item.ID)
	assert.Equal(t, "enc:sk-secret", string(stored.APIKeyEnc), "key 缺省不应修改")
	// 非法更新
	_, err = e.svc.UpdateService(ctx, 1, item.ID, strPtr(""), nil, nil, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.UpdateService(ctx, 1, 9999, nil, nil, nil, nil, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)

	// 删除：存在条目 → MODEL_003
	e.repo.SeedModelEntry(item.ID, model.ModelPurposeChat, "deepseek-chat", false)
	err = e.svc.DeleteService(ctx, 1, item.ID, "")
	assert.Equal(t, service.CodeModelHasEntries, svcErr(t, err).Code)
}

func TestMCEntryAndDefaults(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "DeepSeek", "https://api.deepseek.com/v1", "sk-1", "")
	require.NoError(t, err)

	// 创建默认条目
	item, err := e.svc.CreateEntry(ctx, 1, svcID.ID, "chat", "deepseek-chat", `{"temperature":0.3}`, true, "")
	require.NoError(t, err)
	assert.True(t, item.IsDefault)
	assert.Equal(t, 1, e.env.Audit.CountAction("model.entry_update"))

	// GetDefault：命中并带缓存
	cfg, err := e.svc.GetDefault(ctx, model.ModelPurposeChat)
	require.NoError(t, err)
	assert.Equal(t, "sk-1", cfg.APIKey)
	assert.Equal(t, "https://api.deepseek.com/v1", cfg.BaseURL)
	callsAfterFirst := e.repo.GetDefaultCalls
	cfg2, err := e.svc.GetDefault(ctx, model.ModelPurposeChat)
	require.NoError(t, err)
	assert.Equal(t, cfg, cfg2)
	assert.Equal(t, callsAfterFirst, e.repo.GetDefaultCalls, "缓存命中不应再访问 repo")

	// 写操作失效缓存
	_, err = e.svc.UpdateEntry(ctx, 1, item.ID, nil, strPtr("deepseek-v3"), nil, nil, "")
	require.NoError(t, err)
	assert.Equal(t, 0, e.cache.Size(), "写后缓存应失效")
	cfg3, err := e.svc.GetDefault(ctx, model.ModelPurposeChat)
	require.NoError(t, err)
	assert.Equal(t, "deepseek-v3", cfg3.ModelName)

	// 无默认用途 → MODEL_001
	_, err = e.svc.GetDefault(ctx, model.ModelPurposeEmbedding)
	assert.Equal(t, service.CodeModelNoDefault, svcErr(t, err).Code)

	// 停用持有默认条目的服务 → 422 MODEL_004（审计 P1-6 / PRD FR-12）
	// 修复前这里会成功，随后全站抛 MODEL_001：问答、检索、摄取同时失效。
	_, err = e.svc.UpdateService(ctx, 1, svcID.ID, nil, nil, nil, boolPtr(false), "")
	assert.Equal(t, httpx.CodeModelHasDefaultEntry, svcErr(t, err).Code)
	svcNow, gerr := e.repo.GetServiceByID(ctx, svcID.ID)
	require.NoError(t, gerr)
	assert.True(t, svcNow.Enabled, "拦截后服务应保持启用")

	// 同用途第二个默认：切换后旧的不再是默认
	svc2, err := e.svc.CreateService(ctx, 1, "SiliconFlow", "https://api.siliconflow.cn/v1", "sk-2", "")
	require.NoError(t, err)
	item2, err := e.svc.CreateEntry(ctx, 1, svc2.ID, "chat", "qwen-max", "", true, "")
	require.NoError(t, err)
	got, err := e.repo.GetEntryByID(ctx, item.ID)
	require.NoError(t, err)
	assert.False(t, got.IsDefault, "切换默认后旧条目不再是默认")
	got2, err := e.repo.GetEntryByID(ctx, item2.ID)
	require.NoError(t, err)
	assert.True(t, got2.IsDefault)

	// 默认已切走 → 停用成功，且该用途仍有默认（来自未停用的服务）
	_, err = e.svc.UpdateService(ctx, 1, svcID.ID, nil, nil, nil, boolPtr(false), "")
	require.NoError(t, err)
	cfg4, err := e.svc.GetDefault(ctx, model.ModelPurposeChat)
	require.NoError(t, err)
	assert.Equal(t, "sk-2", cfg4.APIKey, "默认应回落到未停用的服务")

	// 停用服务上直接设默认 → 拒绝
	_, err = e.svc.CreateEntry(ctx, 1, svcID.ID, "embedding", "bge-m3", "", true, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.UpdateService(ctx, 1, svcID.ID, nil, nil, nil, boolPtr(true), "")
	require.NoError(t, err)

	// 默认条目不可删 → MODEL_002
	err = e.svc.DeleteEntry(ctx, 1, item2.ID, "")
	assert.Equal(t, httpx.CodeModelDefaultLocked, svcErr(t, err).Code)

	// 取消默认也不允许（必须切换）
	fv := false
	_, err = e.svc.UpdateEntry(ctx, 1, item2.ID, nil, nil, nil, &fv, "")
	assert.Equal(t, httpx.CodeModelDefaultLocked, svcErr(t, err).Code)

	// 非默认条目可删
	non, err := e.svc.CreateEntry(ctx, 1, svcID.ID, "rerank", "bge-reranker", "", false, "")
	require.NoError(t, err)
	require.NoError(t, e.svc.DeleteEntry(ctx, 1, non.ID, ""))

	// 校验类错误
	_, err = e.svc.CreateEntry(ctx, 1, svcID.ID, "vision", "x", "", false, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.CreateEntry(ctx, 1, svcID.ID, "chat", "x", "not-json", false, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.CreateEntry(ctx, 1, svcID.ID, "chat", "", "", false, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.CreateEntry(ctx, 1, 9999, "chat", "x", "", false, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	_, err = e.svc.UpdateEntry(ctx, 1, 9999, nil, nil, nil, nil, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	err = e.svc.DeleteEntry(ctx, 1, 9999, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
}

func TestMCTestConnection(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "SF", "https://api.siliconflow.cn/v1", "sk-x", "")
	require.NoError(t, err)
	e.repo.SeedModelEntry(svcID.ID, model.ModelPurposeChat, "deepseek-chat", true)
	e.repo.SeedModelEntry(svcID.ID, model.ModelPurposeEmbedding, "bge-m3", false)

	// chat ok / embedding err
	e.tester.EmbedErr = errors.New("模型服务不可用（向量化测试）: dial refused")
	res, err := e.svc.TestConnection(ctx, 1, svcID.ID, "ip")
	require.NoError(t, err)
	assert.Equal(t, "ok", res["chat"])
	assert.Contains(t, res["embedding"], "err:")
	assert.NotContains(t, res, "rerank", "未配置的用途不出现在结果")
	assert.Contains(t, strings.Join(e.tester.Calls, ","), "chat")
	assert.Contains(t, strings.Join(e.tester.Calls, ","), "embedding")

	// 服务不存在
	_, err = e.svc.TestConnection(ctx, 1, 9999, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
}

func TestMCRepoErrorPaths(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	boom := errors.New("db down")
	svcID, err := e.svc.CreateService(ctx, 1, "X", "https://x.com/v1", "k", "")
	require.NoError(t, err)

	// CreateService 存储失败
	e.repo.ErrOnServiceCreate = boom
	_, err = e.svc.CreateService(ctx, 1, "Y", "https://y.com/v1", "k", "")
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnServiceCreate = nil

	// UpdateService 存储失败（回读失败）
	e.repo.ErrOnServiceGet = boom
	_, err = e.svc.UpdateService(ctx, 1, svcID.ID, nil, nil, nil, boolPtr(false), "")
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnServiceGet = nil

	e.repo.ErrOnList = boom
	_, err = e.svc.ListServices(ctx)
	require.ErrorIs(t, err, boom)
	_, err = e.svc.TestConnection(ctx, 1, svcID.ID, "")
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnList = nil

	e.repo.ErrOnServiceSave = boom
	_, err = e.svc.UpdateService(ctx, 1, svcID.ID, nil, nil, nil, boolPtr(false), "")
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnServiceSave = nil

	e.repo.ErrOnServiceDelete = boom
	err = e.svc.DeleteService(ctx, 1, svcID.ID, "")
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnServiceDelete = nil

	e.repo.ErrOnEntryCreate = boom
	_, err = e.svc.CreateEntry(ctx, 1, svcID.ID, "chat", "m", "", false, "")
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnEntryCreate = nil

	e.repo.ErrOnDefault = boom
	_, err = e.svc.GetDefault(ctx, model.ModelPurposeChat)
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnDefault = nil

	// 解密失败（缓存未命中回源路径）：需要存在默认条目
	e.repo.SeedModelEntry(svcID.ID, model.ModelPurposeChat, "m", true)
	e.cipher.FailOnDecrypt = true
	_, err = e.svc.TestConnection(ctx, 1, svcID.ID, "")
	require.ErrorContains(t, err, "解密")
	_, err = e.svc.GetDefault(ctx, model.ModelPurposeChat)
	require.ErrorContains(t, err, "解密")
}

func TestMCUpdateEntryServiceMissing(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "X", "https://x.com/v1", "k", "")
	require.NoError(t, err)
	entryID := e.repo.SeedModelEntry(svcID.ID, model.ModelPurposeChat, "m", false)
	// 条目存在但服务已删除 → 更新返回 NOT_FOUND
	require.NoError(t, e.repo.DeleteService(ctx, svcID.ID))
	_, err = e.svc.UpdateEntry(ctx, 1, entryID, nil, nil, nil, nil, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
}

func TestMCGetDefaultCorruptCache(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "X", "https://x.com/v1", "k", "")
	require.NoError(t, err)
	e.repo.SeedModelEntry(svcID.ID, model.ModelPurposeChat, "m", true)

	// 预置损坏缓存 → 回源
	require.NoError(t, e.cache.Set(ctx, "wp:model-default:1", "not-json", 0))
	cfg, err := e.svc.GetDefault(ctx, model.ModelPurposeChat)
	require.NoError(t, err)
	assert.Equal(t, "k", cfg.APIKey)
	assert.Equal(t, "m", cfg.ModelName)
}

// ---- 辅助 ----

func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }

func jsonOf(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// ---- 边界与分支补测 ----

// TestMCDeleteServiceBranches 删除服务的 NotFound/成功路径与条目计数错误。
func TestMCDeleteServiceBranches(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "X", "https://x.com/v1", "k", "")
	require.NoError(t, err)

	// 不存在 → NOT_FOUND
	err = e.svc.DeleteService(ctx, 1, 9999, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)

	// TestConnection 的解密失败分支（服务存在但 key 解密失败）
	e.cipher.FailOnDecrypt = true
	_, err = e.svc.TestConnection(ctx, 1, svcID.ID, "")
	require.ErrorContains(t, err, "解密")
	e.cipher.FailOnDecrypt = false

	// 无条目 → 成功删除 + 审计
	before := e.env.Audit.CountAction("model.service_update")
	require.NoError(t, e.svc.DeleteService(ctx, 1, svcID.ID, "ip"))
	assert.Greater(t, e.env.Audit.CountAction("model.service_update"), before)
	_, err = e.repo.GetServiceByID(ctx, svcID.ID)
	assert.ErrorIs(t, err, repo.ErrNotFound)
}

// TestMCUpdateEntryValidation 更新条目的参数校验边界。
func TestMCUpdateEntryValidation(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "X", "https://x.com/v1", "k", "")
	require.NoError(t, err)
	entryID := e.repo.SeedModelEntry(svcID.ID, model.ModelPurposeChat, "m", false)

	// purpose 非法
	_, err = e.svc.UpdateEntry(ctx, 1, entryID, strPtr("vision"), nil, nil, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	// 模型名空 / 超长
	_, err = e.svc.UpdateEntry(ctx, 1, entryID, nil, strPtr(" "), nil, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.UpdateEntry(ctx, 1, entryID, nil, strPtr(strings.Repeat("m", 201)), nil, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	// params 非法 JSON 对象（数组）
	bad := "[1,2]"
	_, err = e.svc.UpdateEntry(ctx, 1, entryID, nil, nil, &bad, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 停用服务后设默认 → VALIDATION
	_, err = e.svc.UpdateService(ctx, 1, svcID.ID, nil, nil, nil, boolPtr(false), "")
	require.NoError(t, err)
	_, err = e.svc.UpdateEntry(ctx, 1, entryID, nil, nil, nil, boolPtr(true), "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 非默认条目传 isDefault=false → 正常保存（无副作用）
	updated, err := e.svc.UpdateEntry(ctx, 1, entryID, nil, nil, nil, boolPtr(false), "")
	require.NoError(t, err)
	assert.False(t, updated.IsDefault)
}

// TestMCUpdateEntryDefaultGuards 默认条目守卫与设默认成功路径。
func TestMCUpdateEntryDefaultGuards(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "X", "https://x.com/v1", "k", "")
	require.NoError(t, err)
	defID := e.repo.SeedModelEntry(svcID.ID, model.ModelPurposeChat, "m1", true)
	nonID := e.repo.SeedModelEntry(svcID.ID, model.ModelPurposeChat, "m2", false)

	// 默认条目取消默认 → MODEL_002
	_, err = e.svc.UpdateEntry(ctx, 1, defID, nil, nil, nil, boolPtr(false), "")
	assert.Equal(t, httpx.CodeModelDefaultLocked, svcErr(t, err).Code)

	// 设默认成功：is_default 切换并重读
	updated, err := e.svc.UpdateEntry(ctx, 1, nonID, nil, nil, nil, boolPtr(true), "")
	require.NoError(t, err)
	assert.True(t, updated.IsDefault)

	// 更新不存在的条目 → NOT_FOUND
	_, err = e.svc.UpdateEntry(ctx, 1, 9999, nil, nil, nil, nil, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
}

// TestMCUpdateEntryRepoErrors 更新条目的存储错误分支。
func TestMCUpdateEntryRepoErrors(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "X", "https://x.com/v1", "k", "")
	require.NoError(t, err)
	entryID := e.repo.SeedModelEntry(svcID.ID, model.ModelPurposeChat, "m", false)
	boom := errors.New("db down")

	// SaveEntry 失败
	e.repo.ErrOnEntrySave = boom
	_, err = e.svc.UpdateEntry(ctx, 1, entryID, nil, strPtr("m2"), nil, nil, "")
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnEntrySave = nil

	// 设默认成功但重读失败
	e.repo.ErrOnEntryGet = boom
	_, err = e.svc.UpdateEntry(ctx, 1, entryID, nil, nil, nil, boolPtr(true), "")
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnEntryGet = nil

	// DeleteEntry 存储失败
	e.repo.ErrOnEntryDelete = boom
	err = e.svc.DeleteEntry(ctx, 1, entryID, "")
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnEntryDelete = nil

	// DeleteEntry 不存在 → NOT_FOUND
	err = e.svc.DeleteEntry(ctx, 1, 9999, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)

	// ListEntries 存储失败
	e.repo.ErrOnList = boom
	_, err = e.svc.ListEntries(ctx, svcID.ID, 0)
	require.ErrorIs(t, err, boom)
	e.repo.ErrOnList = nil
}

// TestMCUpdateServiceFields 更新服务的字段替换边界。
func TestMCUpdateServiceFields(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "X", "https://x.com/v1", "sk-old", "")
	require.NoError(t, err)

	// 非法 baseURL → VALIDATION
	bad := "ftp://x.com"
	_, err = e.svc.UpdateService(ctx, 1, svcID.ID, nil, &bad, nil, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	// 超长 baseURL → VALIDATION
	long := "https://" + strings.Repeat("x", 500)
	_, err = e.svc.CreateService(ctx, 1, "L", long, "k", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// apiKey 替换 + 启用切换成功
	newKey := "sk-new"
	updated, err := e.svc.UpdateService(ctx, 1, svcID.ID, nil, nil, &newKey, boolPtr(true), "")
	require.NoError(t, err)
	assert.True(t, updated.Enabled)
	stored, err := e.repo.GetServiceByID(ctx, svcID.ID)
	require.NoError(t, err)
	assert.Equal(t, "enc:sk-new", string(stored.APIKeyEnc), "新 Key 应加密落库")
}

// TestMCCreateEntryValidation 创建条目的参数校验边界。
func TestMCCreateEntryValidation(t *testing.T) {
	e := mcSetup(t)
	ctx := context.Background()
	svcID, err := e.svc.CreateService(ctx, 1, "X", "https://x.com/v1", "k", "")
	require.NoError(t, err)

	// 模型名空 / 超长
	_, err = e.svc.CreateEntry(ctx, 1, svcID.ID, "chat", "", "", false, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.CreateEntry(ctx, 1, svcID.ID, "chat", strings.Repeat("m", 201), "", false, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	// params 非法 JSON
	_, err = e.svc.CreateEntry(ctx, 1, svcID.ID, "chat", "m", "{", false, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	// 默认成功路径（服务启用中）
	item, err := e.svc.CreateEntry(ctx, 1, svcID.ID, "rerank", "bge-reranker", `{"top_n":5}`, true, "ip")
	require.NoError(t, err)
	assert.True(t, item.IsDefault)
	assert.Equal(t, "rerank", item.Purpose)
}
