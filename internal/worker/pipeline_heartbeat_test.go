package worker

// 心跳机制单测（验收回归 8-1）：任务处理期间必须周期性刷新 heartbeat_at，
// 且心跳 goroutine 在 ctx 取消/Process 返回后必须退出（不得泄漏）。
//
// 放在 worker 包内（非 _test 包）是为了注入包级心跳间隔 heartbeatInterval：
// 生产 30s 间隔在单测里等待成本过高。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/testutil"
)

// withFastHeartbeat 把心跳间隔临时调小，测试结束自动还原。
func withFastHeartbeat(t *testing.T, d time.Duration) {
	t.Helper()
	old := heartbeatInterval
	heartbeatInterval = d
	t.Cleanup(func() { heartbeatInterval = old })
}

// blockingEmbedder 卡在向量化阶段，让 Process 保持运行中状态以便观察心跳。
type blockingEmbedder struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingEmbedder) Embed(_ context.Context, _ llm.ResolvedConfig, texts []string) ([][]float32, error) {
	select {
	case <-b.entered:
	default:
		close(b.entered)
	}
	<-b.release
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0.1, 0.2}
	}
	return out, nil
}

// constResolver 固定返回一个 embedding 模型配置。
type constResolver struct{}

func (constResolver) GetDefault(context.Context, int16) (llm.ResolvedConfig, error) {
	return llm.ResolvedConfig{BaseURL: "http://x/v1", APIKey: "k", ModelName: "fake"}, nil
}

// TestHeartbeatLoopRefreshesUntilCancel 心跳立即写一次并按间隔持续刷新；ctx 取消后循环退出。
func TestHeartbeatLoopRefreshesUntilCancel(t *testing.T) {
	withFastHeartbeat(t, 10*time.Millisecond)

	docs := testutil.NewFakeDocs()
	p := NewPipeline(docs, nil, nil, constResolver{}, &blockingEmbedder{}, nil)
	doc := docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusProcessing)
	task := docs.SeedTaskWithWorker(doc.ID, model.TaskTypeFull, model.TaskStatusRunning, "w")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.heartbeatLoop(ctx, task.ID)
	}()

	first := waitHeartbeat(t, docs, task.ID)
	time.Sleep(5 * heartbeatInterval) // 至少再跳 2 次
	second := waitHeartbeat(t, docs, task.ID)
	assert.True(t, second.After(first), "心跳应按间隔持续刷新: first=%v second=%v", first, second)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后心跳 goroutine 必须退出（不得泄漏）")
	}
}

// TestProcessHeartbeatWhileRunning Process 处理期间 heartbeat_at 被刷新（活任务不被时间兜底误判）。
func TestProcessHeartbeatWhileRunning(t *testing.T) {
	withFastHeartbeat(t, 10*time.Millisecond)

	docs := testutil.NewFakeDocs()
	emb := &blockingEmbedder{entered: make(chan struct{}), release: make(chan struct{})}
	p := NewPipeline(docs, nil, nil, constResolver{}, emb, nil)
	uploadDir := t.TempDir()
	p.SetUploadDir(uploadDir)

	doc := docs.SeedDoc(10, model.DocTypeUpload, model.DocStatusPending)
	dir := filepath.Join(uploadDir, "10", fmt.Sprint(doc.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte("# 笔记\n\n正文内容"), 0o600))
	task := docs.SeedTask(doc.ID, model.TaskTypeFull)

	processDone := make(chan error, 1)
	go func() { processDone <- p.Process(context.Background(), task.ID) }()

	select {
	case <-emb.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Process 未进入向量化阶段")
	}

	first := waitHeartbeat(t, docs, task.ID)
	time.Sleep(5 * heartbeatInterval)
	second := waitHeartbeat(t, docs, task.ID)
	assert.True(t, second.After(first), "任务运行期间心跳必须持续刷新")

	close(emb.release)
	select {
	case err := <-processDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Process 未在释放向量化后返回")
	}
	require.Equal(t, model.TaskStatusSuccess, docs.TaskByID(task.ID).Status)
}

// waitHeartbeat 等待任务写入 heartbeat_at 并返回其时刻。
func waitHeartbeat(t *testing.T, docs *testutil.FakeDocRepo, taskID int64) time.Time {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if task := docs.TaskByID(taskID); task != nil && task.HeartbeatAt != nil {
			return *task.HeartbeatAt
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("任务 %d 未在超时前写入心跳", taskID)
	return time.Time{}
}
