// Package mcpserver MCP Server 的工具层与后端 API 客户端（cmd/mcp-server 的核心，可测）。
//
// 职责边界：本包是 woodpecker server 的**独立 HTTP 客户端**——只依赖 API 契约
// （md/API设计.md），不 import internal/service/handler，保持进程级解耦。
// 鉴权为 M15 个人 API Token（Bearer wpk_ 前缀），权限 = token 所属用户本人（R6 矩阵原样生效）。
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client woodpecker server 的轻量 REST 客户端（Bearer API Token 鉴权）。
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

// NewClient 构造客户端；baseURL 形如 http://wp.corp:8080（尾部斜杠会被剥掉）。
func NewClient(baseURL, token string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		hc:      &http.Client{Timeout: timeout},
	}
}

// APIError 后端业务错误（统一响应信封 {code,message,request_id}，API设计.md §1）。
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s(%d): %s", e.Code, e.Status, e.Message)
}

// apiErrorBody 失败响应信封。
type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// do 执行一次 API 调用：2xx 时把响应体 unmarshal 进 dst（可传 nil 忽略）；
// 非 2xx 解析业务错误信封返回 *APIError。
func (c *Client) do(ctx context.Context, method, path string, body, dst any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化请求体: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		var eb apiErrorBody
		if err := json.NewDecoder(resp.Body).Decode(&eb); err != nil {
			// 错误响应体不可解析：保留状态码信息
			return &APIError{Status: resp.StatusCode, Code: "HTTP", Message: resp.Status}
		}
		return &APIError{Status: resp.StatusCode, Code: eb.Code, Message: eb.Message}
	}
	if dst == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("解析响应: %w", err)
	}
	return nil
}
