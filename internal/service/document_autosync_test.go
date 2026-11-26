package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/model"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

// ---- M14 Git 自动同步：定时拉取开关 / webhook 验签 / 扫描入队 ----

func newAutoSyncEnv(t *testing.T) (*service.DocumentService, *testutil.FakeDocRepo, *testutil.FakeQueue) {
	t.Helper()
	base := testutil.NewFakeDocs()
	queue := &testutil.FakeQueue{}
	env := testutil.NewTestEnv(t, nil)
	svc := service.NewDocumentService(base, env.Settings, &testutil.FakeCipher{}, queue, env.AuditSvc)
	return svc, base, queue
}

// TestAutoSyncSetToggle 开关：仅 git 类型；重复设置同值幂等。
func TestAutoSyncSetToggle(t *testing.T) {
	svc, base, _ := newAutoSyncEnv(t)
	ctx := context.Background()
	gitDoc := base.SeedDoc(1, model.DocTypeGit, model.DocStatusReady)
	uploadDoc := base.SeedDoc(1, model.DocTypeUpload, model.DocStatusReady)

	require.NoError(t, svc.SetAutoSync(ctx, 1, 1, gitDoc.ID, true, ""))
	require.True(t, base.DocByID(gitDoc.ID).AutoSync)
	// 非 git 类型 422
	err := svc.SetAutoSync(ctx, 1, 1, uploadDoc.ID, true, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "仅 Git 类型")
	// 幂等：同值再设不报错
	require.NoError(t, svc.SetAutoSync(ctx, 1, 1, gitDoc.ID, true, ""))
	require.NoError(t, svc.SetAutoSync(ctx, 1, 1, gitDoc.ID, false, ""))
	require.False(t, base.DocByID(gitDoc.ID).AutoSync)
}

// TestRotateWebhookSecret 轮换：64 位 hex、加密落库、旧 token 轮换后失效。
func TestRotateWebhookSecret(t *testing.T) {
	svc, base, _ := newAutoSyncEnv(t)
	ctx := context.Background()
	doc := base.SeedDoc(1, model.DocTypeGit, model.DocStatusReady)

	plain1, err := svc.RotateWebhookSecret(ctx, 1, 1, doc.ID, "")
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9a-f]{64}$`, plain1, "32 字节随机 → 64 位 hex")
	stored := base.DocByID(doc.ID)
	require.NotEmpty(t, stored.WebhookSecret)
	assert.NotEqual(t, []byte(plain1), stored.WebhookSecret, "secret 必须加密落库")

	// 非 git 类型 422
	uploadDoc := base.SeedDoc(1, model.DocTypeUpload, model.DocStatusReady)
	_, err = svc.RotateWebhookSecret(ctx, 1, 1, uploadDoc.ID, "")
	require.Error(t, err)

	// 轮换后旧 token 不再有效
	plain2, err := svc.RotateWebhookSecret(ctx, 1, 1, doc.ID, "")
	require.NoError(t, err)
	assert.NotEqual(t, plain1, plain2)
	err = svc.HandleWebhook(ctx, 1, doc.ID, plain1, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DOC_005", "旧 token → 401 DOC_005")
}

// TestHandleWebhook 验签：正确 token 入队 sync；错 token 401；未配置/非 git 404 不泄露。
func TestHandleWebhook(t *testing.T) {
	svc, base, queue := newAutoSyncEnv(t)
	ctx := context.Background()
	doc := base.SeedDoc(1, model.DocTypeGit, model.DocStatusReady)
	plain, err := svc.RotateWebhookSecret(ctx, 1, 1, doc.ID, "")
	require.NoError(t, err)

	// 未配置 webhook 的文档 → 404（不泄露存在性）
	other := base.SeedDoc(1, model.DocTypeGit, model.DocStatusReady)
	err = svc.HandleWebhook(ctx, 1, other.ID, "whatever", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "文档不存在")

	// 错 token → 401
	err = svc.HandleWebhook(ctx, 1, doc.ID, "bad-token", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DOC_005")

	// 正确 token → 任务入队，文档回待处理
	require.NoError(t, svc.HandleWebhook(ctx, 1, doc.ID, plain, ""))
	assert.Len(t, queue.IDs, 1)
	after := base.DocByID(doc.ID)
	assert.Equal(t, model.DocStatusPending, after.Status)
}

// TestAutoSyncAll 扫描：auto_sync 的 git 文档逐个入队；有运行中任务/未开开关/非 git 的跳过。
func TestAutoSyncAll(t *testing.T) {
	svc, base, queue := newAutoSyncEnv(t)
	ctx := context.Background()

	// 四个文档同库（kb=1）：待同步 / 未开开关 / 非 git（开关应 422 拒绝）/ 有运行中任务
	enabled := base.SeedDoc(1, model.DocTypeGit, model.DocStatusReady)
	disabled := base.SeedDoc(1, model.DocTypeGit, model.DocStatusReady)
	uploadDoc := base.SeedDoc(1, model.DocTypeUpload, model.DocStatusReady)
	processing := base.SeedDoc(1, model.DocTypeGit, model.DocStatusReady)
	processing.AutoSync = true // 直接落库（模拟已开开关且任务运行中）
	base.SeedTask(processing.ID, model.TaskTypeSync)

	require.NoError(t, svc.SetAutoSync(ctx, 1, 1, enabled.ID, true, ""))
	require.NoError(t, svc.SetAutoSync(ctx, 1, 1, disabled.ID, false, ""))
	require.Error(t, svc.SetAutoSync(ctx, 1, 1, uploadDoc.ID, true, ""), "非 git 拒绝")

	enqueued, skipped := svc.AutoSyncAll(ctx)
	assert.Equal(t, 1, enqueued, "仅 enabled 入队")
	assert.Equal(t, 1, skipped, "processing（运行中任务）跳过")
	assert.Len(t, queue.IDs, 1)

	// 再跑一轮：enabled 与 processing 的任务都仍 queued（运行中）→ 两个都跳过，幂等不重复入队
	enqueued2, skipped2 := svc.AutoSyncAll(ctx)
	assert.Equal(t, 0, enqueued2)
	assert.Equal(t, 2, skipped2)
	assert.Len(t, queue.IDs, 1)
}
