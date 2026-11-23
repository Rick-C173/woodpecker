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
	// 文档已删除
	require.NoError(t, e.docs.DeleteDocument(context.Background(), d.ID))
	require.NoError(t, e.pipeline.Process(context.Background(), task.ID))
	tk := e.docs.TaskByID(task.ID)
	assert.Equal(t, model.TaskStatusFailed, tk.Status, "删除处理中文档即中止任务")
	assert.Contains(t, *tk.Error, "文档已删除")
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

	e.pipeline.RecoverStartup(ctx, e.docs)
	assert.Equal(t, model.DocStatusFailed, e.docs.DocByID(proc.ID).Status, "处理中文档置失败")
	assert.Equal(t, model.TaskStatusFailed, e.docs.TaskByID(queued.ID).Status, "遗留任务置失败")
}
