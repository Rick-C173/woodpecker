package service_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/httpx"
	"woodpecker/internal/model"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

type docEnv struct {
	docs   *testutil.FakeDocRepo
	queue  *testutil.FakeQueue
	svc    *service.DocumentService
	audit  *testutil.FakeAuditRepo
	upload string
}

func newDocEnv(t *testing.T) *docEnv {
	t.Helper()
	env := testutil.NewTestEnv(t, nil)
	docs := testutil.NewFakeDocs()
	queue := &testutil.FakeQueue{}
	svc := service.NewDocumentService(docs, env.Settings, &testutil.FakeCipher{}, queue, env.AuditSvc)
	dir := t.TempDir()
	svc.SetUploadDir(dir)
	return &docEnv{docs: docs, queue: queue, svc: svc, audit: env.Audit, upload: dir}
}

func TestDocCreateGit(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()

	token := service.GitCredential{Kind: service.CredentialKindToken, Secret: "gh-token"}
	item, err := e.svc.CreateGit(ctx, 1, 10, "https://github.com/x/y.git", "main", token, []string{"*.tmp"}, "ip")
	require.NoError(t, err)
	assert.Equal(t, "git", item.Type)
	assert.Equal(t, "pending", item.Status)
	assert.Len(t, e.queue.IDs, 1, "任务已入队")
	assert.Equal(t, 1, e.audit.CountAction("document.create"))

	stored := e.docs.DocByID(item.ID)
	require.NotNil(t, stored)
	// 凭证按契约 {kind,secret} 序列化后加密落库（审计 P1-12：kind 不再丢失）
	assert.Equal(t, `enc:{"kind":"token","secret":"gh-token"}`, string(stored.CredentialEnc), "凭证加密落库且保留 kind")
	assert.Equal(t, model.StringArray{"*.tmp"}, stored.IgnoreRules)

	// 校验分支
	_, err = e.svc.CreateGit(ctx, 1, 10, "", "", service.GitCredential{}, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.CreateGit(ctx, 1, 10, "ftp://x", "", service.GitCredential{}, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 凭证校验（审计 P1-12）：kind 非法 / ssh 非 PEM / secret 超长 → 422
	_, err = e.svc.CreateGit(ctx, 1, 10, "https://x/y.git", "", service.GitCredential{Kind: "basic", Secret: "s"}, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.CreateGit(ctx, 1, 10, "git@x:y.git", "", service.GitCredential{Kind: service.CredentialKindSSH, Secret: "not-a-key"}, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	_, err = e.svc.CreateGit(ctx, 1, 10, "https://x/y.git", "", service.GitCredential{Kind: service.CredentialKindToken, Secret: strings.Repeat("t", 4097)}, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// ssh:// 前缀被放行（P1-11：SSH 传输层）
	item, err = e.svc.CreateGit(ctx, 1, 10, "ssh://git@git.internal/o/r.git", "", service.GitCredential{}, nil, "")
	require.NoError(t, err, "ssh:// 地址应被接受")
	assert.NotEmpty(t, item.ID)

	// 无 secret 视为匿名：不落凭据
	anon := e.docs.DocByID(item.ID)
	require.NotNil(t, anon)
	assert.Empty(t, anon.CredentialEnc, "匿名接入不应写入 credential_enc")
}

func TestDocCreateUploadAndURL(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()

	files := []service.UploadFile{
		{Filename: "README.md", Content: []byte("# 说明")},
		{Filename: "main.go", Content: []byte("package main")},
		{Filename: "image.png", Content: []byte("png")},
		{Filename: "big.md", Content: make([]byte, 6<<20)},
	}
	items, rejected, err := e.svc.CreateUpload(ctx, 1, 10, files, "ip")
	require.NoError(t, err)
	assert.Len(t, items, 2, "支持格式落库")
	require.Len(t, rejected, 2, "png→DOC_002、超限→DOC_001")
	assert.Equal(t, httpx.CodeDocFormatNoSupport, rejected[0].Code)
	assert.Equal(t, service.CodeDocRepoTooLarge, rejected[1].Code)

	// 文件已落地
	content, rerr := os.ReadFile(filepath.Join(e.upload, "10", strconv.FormatInt(items[0].ID, 10), "README.md"))
	require.NoError(t, rerr)
	assert.Equal(t, "# 说明", string(content))

	// URL
	item, err := e.svc.CreateURL(ctx, 1, 10, "https://docs.example.com/guide", "")
	require.NoError(t, err)
	assert.Equal(t, "url", item.Type)
	_, err = e.svc.CreateURL(ctx, 1, 10, "ftp://x", "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)

	// 空上传
	_, _, err = e.svc.CreateUpload(ctx, 1, 10, nil, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
}

func TestDocKBCapacity(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()
	for i := 0; i < 1000; i++ {
		e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusReady)
	}
	_, err := e.svc.CreateURL(ctx, 1, 10, "https://x.com", "")
	assert.Equal(t, service.CodeDocRepoTooLarge, svcErr(t, err).Code, "R2：库文档数上限 1000")
}

func TestDocListGetDelete(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()
	d1 := e.docs.SeedDoc(10, model.DocTypeGit, model.DocStatusReady)
	e.docs.SeedDoc(11, model.DocTypeGit, model.DocStatusReady) // 其他库

	items, total, err := e.svc.List(ctx, 10, 0, 0, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, items, 1)
	assert.True(t, items[0].CanSync, "git 就绪可同步")
	assert.True(t, items[0].CanReindex)

	// 类型过滤
	_, total, err = e.svc.List(ctx, 10, model.DocTypeUpload, 0, 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// 详情（跨库 → 404）
	_, err = e.svc.Get(ctx, 11, d1.ID)
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	_, err = e.svc.Get(ctx, 10, 9999)
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
	detail, err := e.svc.Get(ctx, 10, d1.ID)
	require.NoError(t, err)
	assert.Equal(t, d1.ID, detail.ID)

	// 删除
	require.NoError(t, e.svc.Delete(ctx, 1, 10, d1.ID, "ip"))
	assert.Equal(t, 1, e.audit.CountAction("document.delete"))
	err = e.svc.Delete(ctx, 1, 10, d1.ID, "")
	assert.Equal(t, service.CodeNotFound, svcErr(t, err).Code)
}

func TestDocRetryReindexSyncGuards(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()

	// Retry：仅失败态
	ready := e.docs.SeedDoc(10, model.DocTypeGit, model.DocStatusReady)
	err := e.svc.Retry(ctx, 1, 10, ready.ID, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	failed := e.docs.SeedDoc(10, model.DocTypeGit, model.DocStatusFailed)
	require.NoError(t, e.svc.Retry(ctx, 1, 10, failed.ID, "ip"))
	assert.Equal(t, 1, e.audit.CountAction("document.retry"))
	require.Len(t, e.queue.IDs, 1)

	// Reindex：仅就绪态（独立文档避免 409）
	err = e.svc.Reindex(ctx, 1, 10, failed.ID, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	reindex := e.docs.SeedDoc(10, model.DocTypeGit, model.DocStatusReady)
	require.NoError(t, e.svc.Reindex(ctx, 1, 10, reindex.ID, "ip"))
	assert.Equal(t, 1, e.audit.CountAction("document.reindex"))

	// Sync：仅 git 类型（独立文档）
	upload := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusReady)
	err = e.svc.Sync(ctx, 1, 10, upload.ID, "")
	assert.Equal(t, service.CodeValidation, svcErr(t, err).Code)
	syncDoc := e.docs.SeedDoc(10, model.DocTypeGit, model.DocStatusReady)
	require.NoError(t, e.svc.Sync(ctx, 1, 10, syncDoc.ID, "ip"))
	assert.Equal(t, 1, e.audit.CountAction("document.sync"))

	// 幂等：存在运行中任务 → 409 DOC_TASK_RUNNING
	e.queue.IDs = nil
	running := e.docs.SeedDoc(10, model.DocTypeGit, model.DocStatusFailed)
	e.docs.SeedTask(running.ID, model.TaskTypeFull)
	err = e.svc.Retry(ctx, 1, 10, running.ID, "")
	assert.Equal(t, service.CodeDocTaskRunning, svcErr(t, err).Code)
	assert.Empty(t, e.queue.IDs, "409 时不再入队")
}

func TestDocRepoErrorPaths(t *testing.T) {
	e := newDocEnv(t)
	ctx := context.Background()
	boom := errors.New("db down")

	e.docs.ErrOnCount = boom
	_, err := e.svc.CreateURL(ctx, 1, 10, "https://x.com", "")
	require.ErrorIs(t, err, boom)
	e.docs.ErrOnCount = nil

	e.docs.ErrOnCreateDoc = boom
	_, err = e.svc.CreateGit(ctx, 1, 10, "https://x/y.git", "", service.GitCredential{}, nil, "")
	require.ErrorIs(t, err, boom)
	e.docs.ErrOnCreateDoc = nil

	e.docs.ErrOnCreateTask = boom
	_, err = e.svc.CreateURL(ctx, 1, 10, "https://x.com", "")
	require.ErrorIs(t, err, boom)
	e.docs.ErrOnCreateTask = nil

	e.docs.ErrOnList = boom
	_, _, err = e.svc.List(ctx, 10, 0, 0, 1, 20)
	require.ErrorIs(t, err, boom)
	e.docs.ErrOnList = nil

	e.docs.ErrOnHistory = boom
	d := e.docs.SeedDoc(10, model.DocTypeGit, model.DocStatusReady)
	_, err = e.svc.Get(ctx, 10, d.ID)
	require.ErrorIs(t, err, boom)
}
