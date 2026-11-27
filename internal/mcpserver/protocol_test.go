package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProtocolEndToEnd 用 InMemoryTransports 走完整 MCP 协议：
// tools/list 应注册四个工具；tools/call search 走通假后端返回文本内容。
func TestProtocolEndToEnd(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/kbs", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"items":[{"id":1,"name":"后端规范","my_role":"owner"}]}`)
	})
	mux.HandleFunc("GET /api/v1/kbs/1/search", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"items":[{"chunk_id":7,"document_id":2,"file_path":"pkg/a.go","line_start":10,"line_end":12,"content":"限流中间件实现"}],"total":1}`)
	})
	ts := newFakeBackend(t, mux)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	srv := mcp.NewServer(&mcp.Implementation{Name: "woodpecker-mcp-test", Version: "test"}, nil)
	Register(srv, FixedClient(NewClient(ts.URL, "wpk_test", 5*time.Second)))
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Run(ctx, serverTransport) }()

	cli := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "v0"}, nil)
	conn, err := cli.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

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

	// tools/call search：走假后端返回文本
	res, err := conn.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"kb_id": float64(1), "query": "限流", "top_k": float64(3)},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "工具调用不应失败")
	require.Len(t, res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "内容应为 TextContent，实际 %T", res.Content[0])
	assert.Contains(t, tc.Text, "pkg/a.go:10-12", "应包含引用标识")
	assert.Contains(t, tc.Text, "限流中间件实现")

	// tools/call list_kbs
	res, err = conn.CallTool(ctx, &mcp.CallToolParams{Name: "list_kbs"})
	require.NoError(t, err)
	require.Len(t, res.Content, 1)
	tc = res.Content[0].(*mcp.TextContent)
	assert.Contains(t, tc.Text, "[1] 后端规范")

	// 服务器端无异常退出
	select {
	case err := <-srvErr:
		t.Fatalf("server 提前退出: %v", err)
	default:
	}
}
