package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeBackend 起一个按路径分发的假 woodpecker 后端；handler 可由用例定制。
func newFakeBackend(t *testing.T, mux *http.ServeMux) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func mustAuth(t *testing.T, r *http.Request) {
	t.Helper()
	auth := r.Header.Get("Authorization")
	require.Equal(t, "Bearer wpk_test", auth, "必须携带 API Token")
}

func TestListKBs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/kbs", func(w http.ResponseWriter, r *http.Request) {
		mustAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[{"id":1,"name":"后端规范","description":"架构文档","my_role":"owner"},{"id":2,"name":"全员可见","my_role":"member"}]}`)
	})
	ts := newFakeBackend(t, mux)

	items, err := NewClient(ts.URL, "wpk_test", 5*time.Second).ListKBs(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, int64(1), items[0].ID)
	assert.Equal(t, "owner", items[0].MyRole)
}

func TestClientErrorPassthrough(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/kbs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"code":"TOK_002","message":"API Token 无效或已吊销/过期"}`)
	})
	ts := newFakeBackend(t, mux)

	_, err := NewClient(ts.URL, "wpk_test", 5*time.Second).ListKBs(context.Background())
	require.Error(t, err)
	var ae *APIError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "TOK_002", ae.Code)
	assert.Equal(t, http.StatusUnauthorized, ae.Status)
	assert.Contains(t, ae.Message, "Token")
}

func TestSearch(t *testing.T) {
	var gotQuery, gotPageSize string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/kbs/3/search", func(w http.ResponseWriter, r *http.Request) {
		mustAuth(t, r)
		gotQuery = r.URL.Query().Get("q")
		gotPageSize = r.URL.Query().Get("page_size")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[{"chunk_id":7,"document_id":1,"file_path":"pkg/a.go","line_start":10,"line_end":20,"content":"限流实现"}],"total":1,"page":1,"page_size":5}`)
	})
	ts := newFakeBackend(t, mux)

	items, err := NewClient(ts.URL, "wpk_test", 5*time.Second).Search(context.Background(), 3, "限流 实现", 5)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "限流 实现", gotQuery) // 空格被 URL 编码后由后端还原
	assert.Equal(t, "5", gotPageSize)
	assert.Equal(t, "pkg/a.go", *items[0].FilePath)
}

func TestSearchClamp(t *testing.T) {
	var gotPageSize string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/kbs/1/search", func(w http.ResponseWriter, r *http.Request) {
		gotPageSize = r.URL.Query().Get("page_size")
		fmt.Fprint(w, `{"items":[]}`)
	})
	ts := newFakeBackend(t, mux)
	c := NewClient(ts.URL, "wpk_test", 5*time.Second)

	_, err := c.Search(context.Background(), 1, "x", 500) // 越界 → clamp 100
	require.NoError(t, err)
	assert.Equal(t, "100", gotPageSize)

	_, err = c.Search(context.Background(), 1, "x", -3) // 非法 → clamp 1
	require.NoError(t, err)
	assert.Equal(t, "1", gotPageSize)
}

// sseBody 通用 SSE 响应写入。
func sseBody(w http.ResponseWriter, events [][2]string) {
	w.Header().Set("Content-Type", "text/event-stream")
	f, _ := w.(http.Flusher)
	for _, ev := range events {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev[0], ev[1])
		if f != nil {
			f.Flush()
		}
	}
}

func TestAskSSEAssembly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/kbs/1/conversations", func(w http.ResponseWriter, r *http.Request) {
		mustAuth(t, r)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":9,"kb_id":1,"title":"x"}`)
	})
	mux.HandleFunc("POST /api/v1/conversations/9/messages", func(w http.ResponseWriter, r *http.Request) {
		mustAuth(t, r)
		var body struct {
			Content string `json:"content"`
		}
		if assert.NoError(t, json.NewDecoder(r.Body).Decode(&body)) && assert.Equal(t, "登录流程是什么", body.Content) {
			sseBody(w, [][2]string{
				{"delta", `{"content":"登录走 JWT，"}`},
				{"ping", `{}`},
				{"citations", `{"items":[{"seq":1,"document_id":1,"chunk_id":7,"file_path":"pkg/auth.go","line_start":10,"line_end":25,"heading_path":null}]}`},
				{"delta", `{"content":"中间件鉴权。"}`},
				{"done", `{"message_id":42}`},
			})
		}
	})
	ts := newFakeBackend(t, mux)

	res, err := NewClient(ts.URL, "wpk_test", 5*time.Second).Ask(context.Background(), 1, "登录流程是什么")
	require.NoError(t, err)
	assert.Equal(t, "登录走 JWT，中间件鉴权。", res.Answer, "ping 心跳帧不得混入答案")
	require.Len(t, res.Citations, 1)
	assert.Equal(t, int64(1), res.Citations[0].Seq)
}

func TestAskSSEErrorEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/kbs/1/conversations", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":9}`)
	})
	mux.HandleFunc("POST /api/v1/conversations/9/messages", func(w http.ResponseWriter, _ *http.Request) {
		sseBody(w, [][2]string{
			{"delta", `{"content":"先给你一半"}`},
			{"error", `{"code":"LLM_001","message":"模型服务不可用"}`},
		})
	})
	ts := newFakeBackend(t, mux)

	_, err := NewClient(ts.URL, "wpk_test", 5*time.Second).Ask(context.Background(), 1, "q")
	require.Error(t, err)
	var ae *APIError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "LLM_001", ae.Code)
}

func TestAskTimeoutPartialAnswer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/kbs/1/conversations", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":9}`)
	})
	mux.HandleFunc("POST /api/v1/conversations/9/messages", func(w http.ResponseWriter, _ *http.Request) {
		sseBody(w, [][2]string{{"delta", `{"content":"部分答案"}`}})
		time.Sleep(800 * time.Millisecond) // 超过客户端 300ms 超时
	})
	ts := newFakeBackend(t, mux)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, err := NewClient(ts.URL, "wpk_test", 10*time.Second).askViaConversation(ctx, 9, "q")
	require.Error(t, err, "超时应报错")
	require.NotNil(t, res)
	assert.Equal(t, "部分答案", res.Answer, "超时返回已拼装的部分答案")
}

func TestAskNonSSEError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/kbs/1/conversations", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":9}`)
	})
	mux.HandleFunc("POST /api/v1/conversations/9/messages", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"code":"RATE_LIMITED","message":"请求过于频繁"}`)
	})
	ts := newFakeBackend(t, mux)

	_, err := NewClient(ts.URL, "wpk_test", 5*time.Second).Ask(context.Background(), 1, "q")
	require.Error(t, err)
	var ae *APIError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "RATE_LIMITED", ae.Code)
}

func TestGetDocument(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/kbs/1/documents/7", func(w http.ResponseWriter, r *http.Request) {
		mustAuth(t, r)
		fmt.Fprint(w, `{"id":7,"name":"架构说明","type":"git","status":"ready","file_count":3,"repo_url":"http://git.corp/x"}`)
	})
	ts := newFakeBackend(t, mux)

	d, err := NewClient(ts.URL, "wpk_test", 5*time.Second).GetDocument(context.Background(), 1, 7)
	require.NoError(t, err)
	assert.Equal(t, "ready", d.Status)
	assert.Equal(t, int64(3), d.FileCount)
}

func TestCiteRef(t *testing.T) {
	fp := "pkg/a.go"
	ls, le := 10, 20
	hp := "设计 > 限流"
	assert.Equal(t, "pkg/a.go:10-20", citeRef(&fp, &ls, &le, nil))
	single := 10
	assert.Equal(t, "pkg/a.go:10", citeRef(&fp, &single, &single, nil))
	assert.Equal(t, "pkg/a.go", citeRef(&fp, nil, nil, nil))
	assert.Equal(t, "设计 > 限流", citeRef(nil, nil, nil, &hp))
	assert.Equal(t, "（无路径引用）", citeRef(nil, nil, nil, nil))
}
