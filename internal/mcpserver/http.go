// http.go Streamable HTTP 传输层（M17，团队共享部署）。
//
// 与 stdio 模式（cmd/mcp-server 独立进程）并存：本文件把同一套工具层（tools.go）
// 挂到 HTTP 端点 /mcp 上，由 woodpecker server 进程直接托管——团队成员的 IDE Agent
// 免安装 exe，直连 http://wp.corp:8080/mcp 并携带个人 API Token。
//
// 传输形态：Stateless（不读/不设 Mcp-Session-Id，每请求临时会话，GET/DELETE 返回 405，
// 对齐 MCP 规范 SEP-2567 会话化方向）+ JSONResponse（响应 application/json 而非 SSE，
// 规避 Nginx proxy_buffering 对 event-stream 的缓冲坑）。
// 无状态模式下 getServer 每个 POST 都会被调用，返回同一 server 实例是合法用法；
// 用户隔离不靠会话而靠 per-request 的 Authorization header（见 HTTPClientResolver）。
package mcpserver

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version MCP 实现版本号（stdio 与 HTTP 两种部署共用）。
const Version = "0.1.0"

// DefaultToolTimeout 单次工具调用超时缺省值（ask 的 LLM 生成较慢，建议 ≥120s）。
const DefaultToolTimeout = 120 * time.Second

// tokenPrefix 个人 API Token 前缀（与 internal/httpx.TokenPrefix 保持同步；
// 本包按契约边界不 import httpx，见包注释）。
const tokenPrefix = "wpk_"

// bearerPrefix Authorization header 的 scheme 前缀。
const bearerPrefix = "Bearer "

// HTTPClientResolver 返回按请求构造客户端的 resolver：从工具调用承载的 HTTP 请求
// （SDK RequestExtra.Header）取 Authorization，剥离 Bearer 前缀后作为该次调用的
// API Token。中间件（middleware.MCPAuth）已做过完整校验，这里的前缀检查是防御兜底——
// stdio 模式不经过本 resolver，Extra 为 nil 时直接报错而非 panic。
func HTTPClientResolver(baseURL string, timeout time.Duration) ClientResolver {
	return func(req *mcp.CallToolRequest) (*Client, error) {
		if req == nil || req.Extra == nil || req.Extra.Header == nil {
			return nil, fmt.Errorf("缺少请求头信息，无法确定调用者")
		}
		auth := req.Extra.Header.Get("Authorization")
		if !strings.HasPrefix(auth, bearerPrefix) {
			return nil, fmt.Errorf("缺少 Bearer Authorization 头（wpk_ 个人 API Token）")
		}
		token := strings.TrimPrefix(auth, bearerPrefix)
		if !strings.HasPrefix(token, tokenPrefix) {
			return nil, fmt.Errorf("MCP 端点仅接受个人 API Token（wpk_ 前缀）")
		}
		return NewClient(baseURL, token, timeout), nil
	}
}

// NewHTTPHandler 构建挂在 /mcp 上的 Streamable HTTP handler。
// selfURL 是本进程 REST API 的自访问地址（工具回环调用 /api/v1，
// 与 stdio exe 的 -server 参数同构）；disableLocalhostProtection 用于
// 「同机 Nginx 反代 + proxy_set_header Host $host」部署（见 md/MCP接入指南.md）。
func NewHTTPHandler(selfURL string, disableLocalhostProtection bool) http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "woodpecker-mcp",
		Title:   "Woodpecker 知识库",
		Version: Version,
	}, nil)
	Register(srv, HTTPClientResolver(selfURL, DefaultToolTimeout))
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			Stateless:                  true,
			JSONResponse:               true,
			Logger:                     slog.Default(),
			DisableLocalhostProtection: disableLocalhostProtection,
		},
	)
}
