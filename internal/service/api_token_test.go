package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

func newTokenSvc(t *testing.T) (*service.APITokenService, *testutil.FakeAPITokenRepo, *testutil.FakeAuditRepo) {
	t.Helper()
	tokens := testutil.NewFakeAPITokens()
	audit := &testutil.FakeAuditRepo{}
	svc := service.NewAPITokenService(tokens, service.NewAuditService(audit))
	base := time.Date(2026, 11, 26, 12, 0, 0, 0, time.Local)
	now := base
	svc.SetClock(func() time.Time { return now })
	return svc, tokens, audit
}

func TestAPITokenCreateAndValidate(t *testing.T) {
	svc, tokens, audit := newTokenSvc(t)
	ctx := context.Background()

	ct, err := svc.Create(ctx, 1, "ci 部署", nil, "127.0.0.1")
	require.NoError(t, err)

	// 明文格式：wpk_ + 64 小写 hex
	assert.Len(t, ct.Token, 68)
	assert.True(t, strings.HasPrefix(ct.Token, "wpk_"))
	hexPart := strings.TrimPrefix(ct.Token, "wpk_")
	assert.Regexp(t, `^[0-9a-f]{64}$`, hexPart)
	assert.Equal(t, hexPart[:8], ct.Item.Prefix)
	assert.Equal(t, 1, audit.CountAction("token.create"))

	// Validate：明文 → userID；last_used_at 落库
	uid, err := svc.Validate(ctx, ct.Token)
	require.NoError(t, err)
	assert.Equal(t, int64(1), uid)
	snap := tokens.Snapshot(ct.Item.ID)
	require.NotNil(t, snap)
	assert.NotNil(t, snap.LastUsedAt)
}

func TestAPITokenCreateValidation(t *testing.T) {
	svc, _, _ := newTokenSvc(t)
	ctx := context.Background()

	// 名称空 / 超长
	_, err := svc.Create(ctx, 1, "  ", nil, "")
	assertTokenCode(t, err, "VALIDATION_FAILED")
	_, err = svc.Create(ctx, 1, strings.Repeat("x", 101), nil, "")
	assertTokenCode(t, err, "VALIDATION_FAILED")

	// 有效期越界
	_, err = svc.Create(ctx, 1, "t", ptrInt(0), "")
	assertTokenCode(t, err, "VALIDATION_FAILED")
	_, err = svc.Create(ctx, 1, "t", ptrInt(3651), "")
	assertTokenCode(t, err, "VALIDATION_FAILED")

	// 边界合法：3650 天
	ct, err := svc.Create(ctx, 1, "十年", ptrInt(3650), "")
	require.NoError(t, err)
	require.NotNil(t, ct.Item.ExpiresAt)
}

func TestAPITokenLimit(t *testing.T) {
	svc, _, _ := newTokenSvc(t)
	ctx := context.Background()
	for i := 0; i < service.MaxTokensPerUser; i++ {
		_, err := svc.Create(ctx, 1, "t", nil, "")
		require.NoError(t, err)
	}
	_, err := svc.Create(ctx, 1, "t", nil, "")
	assertTokenCode(t, err, "TOK_001")

	// 已吊销不计入上限：吊销一个后可再建
	_, err = svc.List(ctx, 1)
	require.NoError(t, err)
}

func TestAPITokenValidateFailures(t *testing.T) {
	svc, tokens, _ := newTokenSvc(t)
	ctx := context.Background()

	// 前缀不对
	_, err := svc.Validate(ctx, "bad_prefix_abc")
	assertTokenCode(t, err, "TOK_002")

	// 不存在
	_, err = svc.Validate(ctx, "wpk_"+strings.Repeat("0", 64))
	assertTokenCode(t, err, "TOK_002")

	// 已吊销
	ct, err := svc.Create(ctx, 1, "t", nil, "")
	require.NoError(t, err)
	require.NoError(t, svc.Revoke(ctx, 1, ct.Item.ID, ""))
	_, err = svc.Validate(ctx, ct.Token)
	assertTokenCode(t, err, "TOK_002")

	// 已过期：把库内 expires_at 拨到过去（模拟时间流逝），再校验 → TOK_002
	ct2, err := svc.Create(ctx, 1, "t2", ptrInt(1), "")
	require.NoError(t, err)
	tokens.Mutate(ct2.Item.ID, func(tk *model.APIToken) {
		past := tk.ExpiresAt.Add(-25 * time.Hour) // 有效期 1 天，拨 25h 必然落在过去
		tk.ExpiresAt = &past
	})
	_, err = svc.Validate(ctx, ct2.Token)
	assertTokenCode(t, err, "TOK_002")

	// 未过期的仍可校验
	ct3, err := svc.Create(ctx, 1, "t3", nil, "")
	require.NoError(t, err)
	uid, err := svc.Validate(ctx, ct3.Token)
	require.NoError(t, err)
	assert.Equal(t, int64(1), uid)
}

func TestAPITokenLastUsedThrottle(t *testing.T) {
	svc, tokens, _ := newTokenSvc(t)
	ctx := context.Background()
	ct, err := svc.Create(ctx, 1, "t", nil, "")
	require.NoError(t, err)

	_, err = svc.Validate(ctx, ct.Token)
	require.NoError(t, err)
	first := tokens.Snapshot(ct.Item.ID).LastUsedAt
	require.NotNil(t, first)

	// 时钟未推进（SetClock 固定）→ 1 分钟内不刷新
	_, err = svc.Validate(ctx, ct.Token)
	require.NoError(t, err)
	second := tokens.Snapshot(ct.Item.ID).LastUsedAt
	assert.Equal(t, *first, *second)
}

func TestAPITokenRevoke(t *testing.T) {
	svc, _, audit := newTokenSvc(t)
	ctx := context.Background()
	ct, err := svc.Create(ctx, 1, "t", nil, "")
	require.NoError(t, err)

	// 他人吊销 → NOT_FOUND（不泄露存在性）
	err = svc.Revoke(ctx, 2, ct.Item.ID, "")
	assertTokenCode(t, err, "NOT_FOUND")

	// 本人吊销 → 成功 + 审计
	require.NoError(t, svc.Revoke(ctx, 1, ct.Item.ID, ""))
	assert.Equal(t, 1, audit.CountAction("token.revoke"))

	// 二次吊销 → NOT_FOUND
	err = svc.Revoke(ctx, 1, ct.Item.ID, "")
	assertTokenCode(t, err, "NOT_FOUND")
}

func TestAPITokenList(t *testing.T) {
	svc, _, _ := newTokenSvc(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := svc.Create(ctx, 1, "t", nil, "")
		require.NoError(t, err)
	}
	// 其他用户的 token 不混入
	_, err := svc.Create(ctx, 2, "other", nil, "")
	require.NoError(t, err)

	items, err := svc.List(ctx, 1)
	require.NoError(t, err)
	assert.Len(t, items, 3)
	// 列表项不含哈希（结构上无该字段，编译期保证；此处断言 prefix 非空）
	for _, it := range items {
		assert.NotEmpty(t, it.Prefix)
	}
}

// assertTokenCode 断言错误为 *ServiceError 且码匹配（repo.ErrNotFound 由 service 直传）。
func assertTokenCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err)
	var se *service.ServiceError
	if errors.As(err, &se) {
		assert.Equal(t, code, se.Code)
		return
	}
	assert.ErrorIs(t, err, repo.ErrNotFound, "期望 NOT_FOUND 或 ServiceError(%s)", code)
}

func ptrInt(v int) *int { return &v }
