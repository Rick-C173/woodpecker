package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

func TestSettingsGetDefaults(t *testing.T) {
	svc := service.NewSettingsService(&testutil.FakeSettingRepo{}, nil)
	v, err := svc.Get(context.Background())
	require.NoError(t, err)
	assert.False(t, v.SelfRegister, "自助注册默认关闭（FR-01）")
	assert.False(t, v.ExclusionCustom, "未自定义排除规则")
	assert.Empty(t, v.ExclusionRules)
}

func TestSettingsUpdateAudit(t *testing.T) {
	fake := &testutil.FakeSettingRepo{Vals: map[string]bool{}}
	env := testutil.NewTestEnv(t, nil)
	svc := service.NewSettingsService(fake, env.AuditSvc)
	ctx := context.Background()

	// 自助注册开启 + 排除规则自定义
	on := true
	rules := []string{"*.secret", " drafts/ ", ""}
	require.NoError(t, svc.Update(ctx, 1, service.UpdateRequest{SelfRegister: &on, ExclusionRules: &rules}, "127.0.0.1"))

	v, err := svc.Get(ctx)
	require.NoError(t, err)
	assert.True(t, v.SelfRegister)
	assert.True(t, v.ExclusionCustom)
	assert.Equal(t, []string{"*.secret", "drafts/"}, v.ExclusionRules, "清洗空白项与首尾空白")

	// 每项一次 setting.update 审计
	assert.Equal(t, 2, env.Audit.CountAction("setting.update"))

	// 空清单恢复默认
	empty := []string{}
	require.NoError(t, svc.Update(ctx, 1, service.UpdateRequest{ExclusionRules: &empty}, ""))
	v, _ = svc.Get(ctx)
	assert.Equal(t, service.DefaultExclusionPatterns(), v.ExclusionRules, "空清单回退默认")
}

func TestSettingsNoop(t *testing.T) {
	fake := &testutil.FakeSettingRepo{Vals: map[string]bool{}}
	env := testutil.NewTestEnv(t, nil)
	svc := service.NewSettingsService(fake, env.AuditSvc)

	// 两项都 nil → 不写入不审计
	require.NoError(t, svc.Update(context.Background(), 1, service.UpdateRequest{}, ""))
	assert.Equal(t, 0, env.Audit.CountAction("setting.update"))
	assert.Empty(t, fake.StrVals)
}
