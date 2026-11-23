package llm_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/llm"
)

func provider() *llm.OpenAIProvider { return llm.NewOpenAIProvider() }

func cfgOf(baseURL string) llm.ResolvedConfig {
	return llm.ResolvedConfig{BaseURL: baseURL, APIKey: "sk-test", ModelName: "test-model", Params: `{"temperature":0.3}`}
}

// mockOpenAI 启动一个 OpenAI 兼容 mock 服务（SSE 流式 + embeddings）。
func mockOpenAI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestChatStream(t *testing.T) {
	ts := mockOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	ch, err := provider().ChatStream(context.Background(), cfgOf(ts.URL), llm.ChatRequest{System: "s", Prompt: "p"})
	require.NoError(t, err)
	var sb strings.Builder
	for d := range ch {
		sb.WriteString(d.Content)
	}
	assert.Equal(t, "你好", sb.String())
}

// TestChatStreamUpstreamError 上游中断（连接被截断）时注入错误增量并关闭通道。
func TestChatStreamUpstreamError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"部分\"}}]}\n\n")
		w.(http.Flusher).Flush()
		// 劫持连接后直接断开，模拟上游中断
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		}
	}))
	t.Cleanup(ts.Close)
	ch, err := provider().ChatStream(context.Background(), cfgOf(ts.URL), llm.ChatRequest{})
	require.NoError(t, err)
	var got []string
	for d := range ch {
		got = append(got, d.Content)
	}
	require.Len(t, got, 2)
	assert.Equal(t, "部分", got[0])
	assert.Contains(t, got[1], "生成中断")
}

// TestChatStreamDialFail 连接不可达 → ErrUnavailable。
func TestChatStreamDialFail(t *testing.T) {
	_, err := provider().ChatStream(context.Background(), cfgOf("http://127.0.0.1:1"), llm.ChatRequest{})
	require.ErrorIs(t, err, llm.ErrUnavailable)
}

// TestEmbed 批量向量化按 index 归位。
func TestEmbed(t *testing.T) {
	ts := mockOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/embeddings", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"index":1,"embedding":[3,4]},{"index":0,"embedding":[1,2]}],"model":"test-model"}`)
	})
	vecs, err := provider().Embed(context.Background(), cfgOf(ts.URL), []string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, vecs, 2)
	assert.Equal(t, []float32{1, 2}, vecs[0])
	assert.Equal(t, []float32{3, 4}, vecs[1])

	// 数量不符 → ErrBadResponse
	ts2 := mockOpenAI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1]}]}`)
	})
	_, err = provider().Embed(context.Background(), cfgOf(ts2.URL), []string{"a", "b"})
	require.ErrorIs(t, err, llm.ErrBadResponse)

	// 空输入
	vecs, err = provider().Embed(context.Background(), cfgOf(ts.URL), nil)
	require.NoError(t, err)
	assert.Nil(t, vecs)
}

// TestRerank 分数按原始 index 归位。
func TestRerank(t *testing.T) {
	var auth string
	ts := mockOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/rerank", r.URL.Path)
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"index":2,"relevance_score":0.9},{"index":0,"relevance_score":0.5}]}`)
	})
	scores, err := provider().Rerank(context.Background(), cfgOf(ts.URL), "q", []string{"a", "b", "c"})
	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-test", auth)
	require.Len(t, scores, 3)
	assert.InDelta(t, 0.5, scores[0], 0.001)
	assert.InDelta(t, 0.0, scores[1], 0.001)
	assert.InDelta(t, 0.9, scores[2], 0.001)

	// 非 200 → ErrBadResponse
	ts2 := mockOpenAI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	})
	_, err = provider().Rerank(context.Background(), cfgOf(ts2.URL), "q", []string{"a"})
	require.ErrorIs(t, err, llm.ErrBadResponse)

	// 连接失败 → ErrUnavailable
	_, err = provider().Rerank(context.Background(), cfgOf("http://127.0.0.1:1"), "q", []string{"a"})
	assert.ErrorIs(t, err, llm.ErrUnavailable)
}

// TestTesterImplementations 三项连通性测试。
func TestTesterImplementations(t *testing.T) {
	ts := mockOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
		case "/embeddings":
			fmt.Fprint(w, `{"data":[{"index":0,"embedding":[1]}]}`)
		case "/rerank":
			fmt.Fprint(w, `{"results":[{"index":0,"relevance_score":0.5}]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	p := provider()
	cfg := cfgOf(ts.URL)
	assert.NoError(t, p.TestChat(context.Background(), cfg))
	assert.NoError(t, p.TestEmbedding(context.Background(), cfg))
	assert.NoError(t, p.TestRerank(context.Background(), cfg))

	bad := cfgOf("http://127.0.0.1:1")
	assert.ErrorIs(t, p.TestChat(context.Background(), bad), llm.ErrUnavailable)
}

// TestChatStreamCtxCancel ctx 取消后通道应关闭。
func TestChatStreamCtxCancel(t *testing.T) {
	release := make(chan struct{})
	ts := mockOpenAI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-release
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ch, err := provider().ChatStream(ctx, cfgOf(ts.URL), llm.ChatRequest{})
	require.NoError(t, err)
	var n int
	for range ch {
		n++
	}
	assert.GreaterOrEqual(t, n, 1)
	close(release)
}
