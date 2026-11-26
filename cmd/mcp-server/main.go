// Woodpecker MCP Server 入口（cmd/mcp-server）：stdio 传输的独立进程。
//
// 用法（IDE 的 mcp.json 配置示例见 md/MCP接入指南.md）：
//
//	woodpecker-mcp -server http://wp.corp:8080 -token wpk_xxx
//
// 参数可用环境变量兜底（WOODPECKER_SERVER / WOODPECKER_TOKEN）；
// ⚠ stdio 传输下 stdout 是 JSON-RPC 协议通道——一切日志只写 stderr。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"woodpecker/internal/mcpserver"
)

const version = "0.1.0"

func main() {
	server := flag.String("server", envOr("WOODPECKER_SERVER", ""), "woodpecker 服务地址，如 http://wp.corp:8080")
	token := flag.String("token", envOr("WOODPECKER_TOKEN", ""), "个人 API Token（wpk_ 前缀，网页端个人设置生成）")
	timeout := flag.Duration("timeout", envDurationOr("WOODPECKER_TIMEOUT", 120*time.Second), "单次工具调用超时（ask 的 LLM 生成较慢，建议 ≥120s）")
	flag.Parse()

	if *server == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "usage: woodpecker-mcp -server <url> -token <wpk_...>")
		fmt.Fprintln(os.Stderr, "  也可用环境变量 WOODPECKER_SERVER / WOODPECKER_TOKEN（命令行优先）")
		os.Exit(2)
	}

	// 日志只走 stderr：stdout 是 MCP 协议通道，污染即断连
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "woodpecker-mcp",
		Title:   "Woodpecker 知识库",
		Version: version,
	}, nil)
	mcpserver.Register(srv, mcpserver.NewClient(*server, *token, *timeout))

	slog.Info("woodpecker-mcp 启动", "server", *server, "timeout", *timeout)
	ctx := context.Background()
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		slog.Error("mcp server 退出", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDurationOr(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
