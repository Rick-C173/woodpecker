package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"woodpecker/internal/middleware"
)

// bearerRT 模拟 MCP 客户端的自定义 header 能力：为所有请求注入 Authorization
// （Claude Code / Cursor / Trae 的 HTTP MCP 配置均支持 headers 字段）。
type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}

// connectMCP 建立 Streamable HTTP 客户端连接。stateless 服务器不返回
// Mcp-Session-Id，客户端自动省略该 header 并跳过 DELETE 语义。
func connectMCP(ctx context.Context, t *testing.T, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	cli := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "v0"}, nil)
	conn, err := cli.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: bearerRT{token: token}},
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() }) // stateless 下 DELETE 收 405，忽略
	return conn
}

// newMCPServer 起一个挂 NewHTTPHandler 的裸 httptest server（不带鉴权中间件——
// 鉴权由各用例自行叠加；token 有效性由假后端 mustAuth 断言）。
func newMCPServer(t *testing.T, selfURL string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(NewHTTPHandler(selfURL, false))
	t.Cleanup(ts.Close)
	return ts
}

func TestHTTPFullChain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/kbs", func(w http.ResponseWriter, r *http.Request) {
		mustAuth(t, r)
		fmt.Fprint(w, `{"items":[{"id":1,"name":"后端规范","my_role":"owner"}]}`)
	})
	mux.HandleFunc("GET /api/v1/kbs/1/search", func(w http.ResponseWriter, r *http.Request) {
		mustAuth(t, r)
		fmt.Fprint(w, `{"items":[{"chunk_id":7,"document_id":2,"file_path":"pkg/a.go","line_start":10,"line_end":12,"content":"限流中间件实现"}],"total":1}`)
	})
	ts := newFakeBackend(t, mux)
	mcpSrv := newMCPServer(t, ts.URL)

	ctx := context.Background()
	conn := connectMCP(ctx, t, mcpSrv.URL, "wpk_test")

	// tools/list：四工具齐全
	tools, err := conn.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(tools.Tools))
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	for _, want := range []string{"list_kbs", "search", "ask", "get_document"} {
		assert.Contains(t, names, want)
	}

	// tools/call search：回环调用假后端，带引用文本返回
	res, err := conn.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"kb_id": float64(1), "query": "限流", "top_k": float64(3)},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "工具调用不应失败")
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "内容应为 TextContent，实际 %T", res.Content[0])
	assert.Contains(t, tc.Text, "pkg/a.go:10-12")
	assert.Contains(t, tc.Text, "限流中间件实现")

	// JSONResponse:true：直发裸 tools/list 应得到 application/json 而非 SSE
	req, _ := http.NewRequest(http.MethodPost, mcpSrv.URL, strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer wpk_test")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "application/json",
		"JSONResponse 模式必须返回 application/json（规避反代 SSE 缓冲坑）")
}

func TestTokenPerRequest(t *testing.T) {
	var mu sync.Mutex
	var got []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/kbs", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Header.Get("Authorization"))
		mu.Unlock()
		fmt.Fprint(w, `{"items":[]}`)
	})
	ts := newFakeBackend(t, mux)
	mcpSrv := newMCPServer(t, ts.URL)

	ctx := context.Background()
	for _, token := range []string{"wpk_alice", "wpk_bob"} {
		conn := connectMCP(ctx, t, mcpSrv.URL, token)
		_, err := conn.CallTool(ctx, &mcp.CallToolParams{Name: "list_kbs"})
		require.NoError(t, err)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 2)
	assert.Equal(t, "Bearer wpk_alice", got[0])
	assert.Equal(t, "Bearer wpk_bob", got[1], "第二个连接必须携带自己的 token（per-request 客户端，无跨会话缓存）")
}

// TestHTTPAuthRejects 中间件层鉴权三态：无 token / JWT 形态 / 无效 wpk_ token 均应 401 TOK_002
// （MCP 端点仅接受个人 API Token，拒绝 JWT——机器凭证语义）。
func TestHTTPAuthRejects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	lookup := func(_ *gin.Context, bearer string) (int64, error) {
		if bearer == "wpk_good" {
			return 42, nil
		}
		return 0, fmt.Errorf("invalid token")
	}
	r.Any("/mcp", middleware.MCPAuth(lookup), gin.WrapH(NewHTTPHandler("http://127.0.0.1:1", false)))

	cases := []struct {
		name   string
		auth   string // 空 = 不带 header
		status int
	}{
		{"无 Authorization", "", http.StatusUnauthorized},
		{"JWT 形态被拒", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig", http.StatusUnauthorized},
		{"无效 token", "Bearer wpk_bad", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"v0"}}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code)
			assert.Contains(t, w.Body.String(), "TOK_002")
		})
	}
}

func TestStatelessGET(t *testing.T) {
	mcpSrv := newMCPServer(t, "http://127.0.0.1:1")

	resp, err := http.Get(mcpSrv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode, "stateless 模式 GET 返回 405")
	assert.Equal(t, "POST", resp.Header.Get("Allow"), "RFC 9110 要求 405 携带 Allow 头")
}

// TestHTTPClientResolver resolver 防御分支：stdio（Extra nil）与非 wpk_ 凭证应报错而非 panic。
func TestHTTPClientResolver(t *testing.T) {
	resolver := HTTPClientResolver("http://127.0.0.1:1", DefaultToolTimeout)
	withAuth := func(v string) *mcp.CallToolRequest {
		return &mcp.CallToolRequest{Extra: &mcp.RequestExtra{
			Header: http.Header{"Authorization": []string{v}},
		}}
	}

	c, err := resolver(&mcp.CallToolRequest{})
	require.Error(t, err, "Extra 为 nil（stdio 误用）必须报错")
	assert.Nil(t, c)

	c, err = resolver(withAuth("Bearer eyJhbGciOiJIUzI1NiJ9.sig"))
	require.Error(t, err, "JWT 形态必须报错")
	assert.Nil(t, c)

	c, err = resolver(withAuth("Bearer wpk_ok"))
	require.NoError(t, err)
	require.NotNil(t, c)
}
