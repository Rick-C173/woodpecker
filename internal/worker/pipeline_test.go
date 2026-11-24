package worker_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/testutil"
	"woodpecker/internal/worker"
)

type fakeQueue struct {
	mu    sync.Mutex
	tasks []*asynq.Task
	err   error
}

func (f *fakeQueue) EnqueueContext(_ context.Context, task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, task)
	return &asynq.TaskInfo{ID: "fake"}, nil
}

type fakeEmbedder struct {
	batches []int
	err     error
}

func (f *fakeEmbedder) Embed(_ context.Context, _ llm.ResolvedConfig, texts []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.batches = append(f.batches, len(texts))
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0.1, 0.2}
	}
	return out, nil
}

type fakeResolver struct{ err error }

func (f *fakeResolver) GetDefault(_ context.Context, purpose int16) (llm.ResolvedConfig, error) {
	if f.err != nil {
		return llm.ResolvedConfig{}, f.err
	}
	if purpose != model.ModelPurposeEmbedding {
		return llm.ResolvedConfig{}, errors.New("只应取 embedding 配置")
	}
	return llm.ResolvedConfig{BaseURL: "http://x/v1", APIKey: "k", ModelName: "bge-m3"}, nil
}

// newTestPage 启动一个返回固定 HTML 的本地页面（URL 拉取测试用）。
func newTestPage(t *testing.T, html string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}))
	t.Cleanup(ts.Close)
	return ts
}

type pipeEnv struct {
	docs     *testutil.FakeDocRepo
	queue    *fakeQueue
	embed    *fakeEmbedder
	resolver *fakeResolver
	pipeline *worker.Pipeline
	upload   string
}

func newPipeEnv(t *testing.T) *pipeEnv {
	t.Helper()
	docs := testutil.NewFakeDocs()
	queue := &fakeQueue{}
	embed := &fakeEmbedder{}
	resolver := &fakeResolver{}
	p := worker.NewPipeline(docs, &testutil.FakeSettingRepo{}, &testutil.FakeCipher{}, resolver, embed, queue)
	dir := t.TempDir()
	p.SetUploadDir(dir)
	return &pipeEnv{docs: docs, queue: queue, embed: embed, resolver: resolver, pipeline: p, upload: dir}
}

func (e *pipeEnv) resolverErr() { e.resolver.err = errors.New("MODEL_001") }

func TestPipelineUploadSuccess(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()

	// 上传文档：落地两个文件
	d := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusPending)
	dir := filepath.Join(e.upload, "10", fmt.Sprint(d.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# 指南\n\n内容"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc A() {}\n"), 0o600))
	secret := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(secret, []byte("KEY=1"), 0o600))

	task := e.docs.SeedTask(d.ID, model.TaskTypeFull)
	require.NoError(t, e.pipeline.Process(ctx, task.ID))

	doc := e.docs.DocByID(d.ID)
	assert.Equal(t, model.DocStatusReady, doc.Status, "状态走完")
	assert.Nil(t, doc.Stage)
	assert.NotNil(t, doc.FileCount)
	assert.Equal(t, 3, *doc.FileCount, "2 个索引 + 1 个排除")
	assert.NotNil(t, doc.TotalSize)
	finished := e.docs.TaskByID(task.ID)
	assert.Equal(t, model.TaskStatusSuccess, finished.Status)
	require.Len(t, e.embed.batches, 1, "批量向量化调用")
}

func TestPipelineDocDeleted(t *testing.T) {
	e := newPipeEnv(t)
	d := e.docs.SeedDoc(10, model.DocTypeURL, model.DocStatusPending)
	task := e.docs.SeedTask(d.ID, model.TaskTypeFull)
	// 文档已删除（真库为 ON DELETE CASCADE，任务行随之消失；替身同口径）
	require.NoError(t, e.docs.DeleteDocument(context.Background(), d.ID))
	require.NoError(t, e.pipeline.Process(context.Background(), task.ID))
	assert.Nil(t, e.docs.TaskByID(task.ID), "任务随文档级联删除，不返回临时错误（asynq 不再重投）")
}

func TestPipelineRetryThenFinalFail(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()

	d := e.docs.SeedDoc(10, model.DocTypeURL, model.DocStatusPending)
	unreachable := "http://127.0.0.1:1"
	d.SourceURL = &unreachable
	require.NoError(t, e.docs.SaveDocument(ctx, d))
	task := e.docs.SeedTask(d.ID, model.TaskTypeFull)

	// 三次失败 → 三次重排（R3）
	require.NoError(t, e.pipeline.Process(ctx, task.ID))
	require.NoError(t, e.pipeline.Process(ctx, task.ID))
	require.NoError(t, e.pipeline.Process(ctx, task.ID))
	assert.Len(t, e.queue.tasks, 3, "重排 3 次")
	tk := e.docs.TaskByID(task.ID)
	assert.Equal(t, 3, tk.Retries)
	assert.Equal(t, model.TaskStatusQueued, tk.Status, "重排后复位为排队")

	// 第四次：达到上限 → 文档与任务置失败
	require.NoError(t, e.pipeline.Process(ctx, task.ID))
	assert.Len(t, e.queue.tasks, 3, "不再重排")
	doc := e.docs.DocByID(d.ID)
	assert.Equal(t, model.DocStatusFailed, doc.Status)
	require.NotNil(t, doc.Error)
	assert.Contains(t, *doc.Error, "DOC_003")
	tk = e.docs.TaskByID(task.ID)
	assert.Equal(t, model.TaskStatusFailed, tk.Status)
}

func TestPipelineEmbedNoDefaultModel(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()

	// 本地上传文档：拉取成功 → 向量化阶段因缺省模型失败
	e.resolverErr()
	d := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusPending)
	dir := filepath.Join(e.upload, "10", fmt.Sprint(d.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte("# 笔记\n\n正文"), 0o600))
	task := e.docs.SeedTask(d.ID, model.TaskTypeFull)
	for i := 0; i < 4; i++ {
		require.NoError(t, e.pipeline.Process(ctx, task.ID))
	}
	doc := e.docs.DocByID(d.ID)
	assert.Equal(t, model.DocStatusFailed, doc.Status)
	require.NotNil(t, doc.Error)
	assert.Contains(t, *doc.Error, "获取默认向量化模型")
}

func TestPipelineURLSuccess(t *testing.T) {
	e := newPipeEnv(t)
	// 用本地 httptest 页面
	ts := newTestPage(t, "<html><head><title>指南</title></head><body><nav>导航</nav><p>正文内容 Hello</p></body></html>")
	defer ts.Close()

	d := e.docs.SeedDoc(10, model.DocTypeURL, model.DocStatusPending)
	src := ts.URL
	d.SourceURL = &src
	require.NoError(t, e.docs.SaveDocument(context.Background(), d))
	task := e.docs.SeedTask(d.ID, model.TaskTypeFull)
	require.NoError(t, e.pipeline.Process(context.Background(), task.ID))

	doc := e.docs.DocByID(d.ID)
	assert.Equal(t, model.DocStatusReady, doc.Status)
	assert.Equal(t, 1, e.docs.FileCount(d.ID), "文件清单一条")
}

func TestPipelineRecoverStartup(t *testing.T) {
	e := newPipeEnv(t)
	ctx := context.Background()
	proc := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	queued := e.docs.SeedTask(proc.ID, model.TaskTypeFull)
	queued.Status = model.TaskStatusRunning
	require.NoError(t, e.docs.SaveTask(ctx, queued))
	// 崩溃发生在 1 小时前：心跳/开始时间远早于僵尸阈值（验收回归 8-1 的时间兜底依据）
	e.docs.BackdateTask(queued.ID, time.Hour)

	e.pipeline.RecoverStartup(ctx, e.docs)
	assert.Equal(t, model.DocStatusFailed, e.docs.DocByID(proc.ID).Status, "处理中文档置失败")
	assert.Equal(t, model.TaskStatusFailed, e.docs.TaskByID(queued.ID).Status, "遗留任务置失败")
}

// ---- 增量同步三态（P0-1 回归，替身已改为真实内存实现）----
//
// 这三条用例在 FakeDocRepo.ReplaceDocumentChunks / DeleteChunksByFile 为「空实现」时
// 原理上测不出缺陷（审计 §3.4 第 1 条），因此与替身改造同批提交。

// seedSyncDoc 落地 files 到上传目录并建一个上传文档（不进数据库，走 FakeDocRepo）。
func (e *pipeEnv) seedSyncDoc(t *testing.T, files map[string]string) *model.Document {
	t.Helper()
	d := e.docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusPending)
	dir := filepath.Join(e.upload, "10", fmt.Sprint(d.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
	}
	return d
}

func (e *pipeEnv) runTask(t *testing.T, docID int64, typ int16) {
	t.Helper()
	task := e.docs.SeedTask(docID, typ)
	require.NoError(t, e.pipeline.Process(context.Background(), task.ID))
	assert.Equal(t, model.TaskStatusSuccess, e.docs.TaskByID(task.ID).Status, "任务应成功")
}

// 三态①：全未变 —— 分块必须原样保留（旧实现整篇清空）。
func TestPipelineSyncNoChangeKeepsChunks(t *testing.T) {
	e := newPipeEnv(t)
	d := e.seedSyncDoc(t, map[string]string{
		"README.md": "# 指南\n\n内容\n",
		"main.go":   "package main\n\nfunc A() {}\n",
	})
	e.runTask(t, d.ID, model.TaskTypeFull)
	fullA := e.docs.ChunkCountByFile(d.ID, "README.md")
	fullB := e.docs.ChunkCountByFile(d.ID, "main.go")
	require.NotZero(t, fullA)
	require.NotZero(t, fullB)
	total := len(e.docs.GetChunks(d.ID))
	embedCallsBefore := len(e.embed.batches)

	e.runTask(t, d.ID, model.TaskTypeSync)

	assert.Len(t, e.docs.GetChunks(d.ID), total, "无变更的同步不得改动任何分块")
	assert.Equal(t, fullA, e.docs.ChunkCountByFile(d.ID, "README.md"))
	assert.Equal(t, fullB, e.docs.ChunkCountByFile(d.ID, "main.go"))
	assert.Len(t, e.embed.batches, embedCallsBefore, "无变更时不应调用 embedding")
}

// 三态②：部分变更 —— 只替换变更文件，未变更文件的分块原样保留。
func TestPipelineSyncPartialChangeKeepsOtherFiles(t *testing.T) {
	e := newPipeEnv(t)
	d := e.seedSyncDoc(t, map[string]string{
		"a.md": "# A\n\nA 文件内容。\n",
		"b.md": "# B\n\nB 文件内容。\n",
		"c.md": "# C\n\nC 文件内容。\n",
	})
	e.runTask(t, d.ID, model.TaskTypeFull)
	beforeB := e.docs.GetChunks(d.ID)
	require.Equal(t, 1, e.docs.ChunkCountByFile(d.ID, "b.md"))
	require.Equal(t, 1, e.docs.ChunkCountByFile(d.ID, "c.md"))
	require.Equal(t, 1, e.docs.ChunkCountByFile(d.ID, "a.md"))

	require.NoError(t, os.WriteFile(filepath.Join(e.upload, "10", fmt.Sprint(d.ID), "a.md"),
		[]byte("# A\n\nA 文件内容（已修改）。\n"), 0o600))
	e.runTask(t, d.ID, model.TaskTypeSync)

	after := e.docs.GetChunks(d.ID)
	assert.Len(t, after, 3, "3 个文件各 1 块")
	assert.Equal(t, 1, e.docs.ChunkCountByFile(d.ID, "b.md"), "未变更文件 b.md 的分块必须保留")
	assert.Equal(t, 1, e.docs.ChunkCountByFile(d.ID, "c.md"), "未变更文件 c.md 的分块必须保留")
	for _, c := range after {
		if c.FilePath == nil || *c.FilePath != "a.md" {
			continue
		}
		assert.Contains(t, c.Content, "已修改", "a.md 应被重切")
	}
	// b.md 的分块内容逐字未变
	want := ""
	for _, c := range beforeB {
		if c.FilePath != nil && *c.FilePath == "b.md" {
			want = c.Content
		}
	}
	got := ""
	for _, c := range after {
		if c.FilePath != nil && *c.FilePath == "b.md" {
			got = c.Content
		}
	}
	assert.Equal(t, want, got, "未变更文件的分块内容必须逐字不变")
}

// 三态③：远端删除 —— 被删文件的分块清除，其余文件不受影响。
func TestPipelineSyncRemoteDeleteRemovesOnlyThatFile(t *testing.T) {
	e := newPipeEnv(t)
	d := e.seedSyncDoc(t, map[string]string{
		"a.md": "# A\n\nA 文件内容。\n",
		"b.md": "# B\n\nB 文件内容。\n",
	})
	e.runTask(t, d.ID, model.TaskTypeFull)
	require.Equal(t, 1, e.docs.ChunkCountByFile(d.ID, "a.md"))
	require.Equal(t, 1, e.docs.ChunkCountByFile(d.ID, "b.md"))

	require.NoError(t, os.Remove(filepath.Join(e.upload, "10", fmt.Sprint(d.ID), "a.md")))
	e.runTask(t, d.ID, model.TaskTypeSync)

	assert.Zero(t, e.docs.ChunkCountByFile(d.ID, "a.md"), "已删除文件的分块应清除")
	assert.Equal(t, 1, e.docs.ChunkCountByFile(d.ID, "b.md"), "未变更文件的分块必须保留")
	assert.Equal(t, 1, e.docs.FileCount(d.ID), "文件清单应只剩 b.md")
}

// 三态④：变更文件 embedding 失败 —— 旧块必须保留（旧实现先删后 embed，会丢内容）。
func TestPipelineSyncEmbedFailureKeepsOldChunks(t *testing.T) {
	e := newPipeEnv(t)
	d := e.seedSyncDoc(t, map[string]string{"a.md": "# A\n\nA 文件内容。\n"})
	e.runTask(t, d.ID, model.TaskTypeFull)
	require.Equal(t, 1, e.docs.ChunkCountByFile(d.ID, "a.md"))

	require.NoError(t, os.WriteFile(filepath.Join(e.upload, "10", fmt.Sprint(d.ID), "a.md"),
		[]byte("# A\n\nA 文件内容（已修改）。\n"), 0o600))
	e.embed.err = errors.New("embedding 服务不可用")
	task := e.docs.SeedTask(d.ID, model.TaskTypeSync)
	require.NoError(t, e.pipeline.Process(context.Background(), task.ID))
	assert.Equal(t, model.TaskStatusQueued, e.docs.TaskByID(task.ID).Status, "失败后应排重试")

	chunks := e.docs.GetChunks(d.ID)
	require.Len(t, chunks, 1, "embedding 失败不得删除旧分块")
	assert.NotContains(t, chunks[0].Content, "已修改", "旧分块应保持原内容（问答期间不受影响）")
}
