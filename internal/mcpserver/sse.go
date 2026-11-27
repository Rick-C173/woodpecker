package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ---- API 契约 DTO（仅本包所需字段；字段名与后端 JSON 契约逐一对齐）----

// KBItem 知识库条目（GET /api/v1/kbs，响应 {items:[...]}）。
type KBItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MyRole      string `json:"my_role"` // owner/editor/member（全员可见库对非成员为 member）
}

type kbList struct {
	Items []KBItem `json:"items"`
}

// ListKBs 列出 token 用户可见（有权 + 全员可见）的知识库。
func (c *Client) ListKBs(ctx context.Context) ([]KBItem, error) {
	var out kbList
	if err := c.do(ctx, http.MethodGet, "/api/v1/kbs", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// SearchItem 检索命中（GET /api/v1/kbs/{kb}/search）。
type SearchItem struct {
	ChunkID     int64   `json:"chunk_id"`
	DocumentID  int64   `json:"document_id"`
	FilePath    *string `json:"file_path"`
	LineStart   *int    `json:"line_start"`
	LineEnd     *int    `json:"line_end"`
	HeadingPath *string `json:"heading_path"`
	Content     string  `json:"content"`
	Score       float64 `json:"score"`
}

type searchResult struct {
	Items []SearchItem `json:"items"`
}

// Search 混合检索（RRF + 可选 rerank）；topK 由调用方 clamp 到 [1,100]。
func (c *Client) Search(ctx context.Context, kbID int64, query string, topK int) ([]SearchItem, error) {
	if topK < 1 {
		topK = 1
	}
	if topK > 100 {
		topK = 100 // 后端 page_size 上限 100，越界静默回落 20——客户端必须自行 clamp
	}
	var out searchResult
	path := fmt.Sprintf("/api/v1/kbs/%d/search?q=%s&page=1&page_size=%d", kbID, url.QueryEscape(query), topK)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Conversation 建会话响应（POST /api/v1/kbs/{kb}/conversations，201）。
type Conversation struct {
	ID    int64  `json:"id"`
	KBID  int64  `json:"kb_id"`
	Title string `json:"title"`
}

// CreateConversation 在知识库下新建问答会话。
func (c *Client) CreateConversation(ctx context.Context, kbID int64) (*Conversation, error) {
	var out Conversation
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/kbs/%d/conversations", kbID), map[string]any{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Citation 问答引用（SSE citations 事件；字段与 service.CitationItem 对齐）。
type Citation struct {
	Seq         int64   `json:"seq"`
	DocumentID  int64   `json:"document_id"`
	ChunkID     int64   `json:"chunk_id"`
	FilePath    *string `json:"file_path"`
	LineStart   *int    `json:"line_start"`
	LineEnd     *int    `json:"line_end"`
	HeadingPath *string `json:"heading_path"`
}

// DocumentDetail 文档详情（GET /api/v1/kbs/{kb}/documents/{doc}）。
type DocumentDetail struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Stage     string `json:"stage"`
	Error     string `json:"error"`
	FileCount int64  `json:"file_count"`
	RepoURL   string `json:"repo_url"`
	SourceURL string `json:"source_url"`
}

// GetDocument 取文档详情（文件清单与状态；v1 不含 chunk 全文，全文走 search）。
func (c *Client) GetDocument(ctx context.Context, kbID, docID int64) (*DocumentDetail, error) {
	var out DocumentDetail
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/kbs/%d/documents/%d", kbID, docID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AskResult ask 工具的最终输出。
type AskResult struct {
	Answer    string // 流式 delta 拼接的完整答案
	Citations []Citation
}

// askViaConversation 在既有会话中提问并消费 SSE 流，拼装非流式答案。
//
// SSE 协议（internal/handler/chat.go）：`event: <名>` + `data: <JSON>`，空行分隔事件；
// 事件名 delta/citations/done/error，ping 为 15s 心跳。读到 done 正常返回；
// error 事件返回业务错误；流中断/ctx 超时返回已拼装的部分答案（服务端照常落库，无副作用）。
func (c *Client) askViaConversation(ctx context.Context, convID int64, question string) (*AskResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+fmt.Sprintf("/api/v1/conversations/%d/messages", convID),
		strings.NewReader(fmt.Sprintf(`{"content":%q}`, question)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	// 非 SSE（4xx JSON 信封或代理错误页）：走统一错误解析
	if resp.StatusCode >= 300 || !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		var eb apiErrorBody
		if jerr := json.NewDecoder(resp.Body).Decode(&eb); jerr == nil && eb.Code != "" {
			return nil, &APIError{Status: resp.StatusCode, Code: eb.Code, Message: eb.Message}
		}
		return nil, &APIError{Status: resp.StatusCode, Code: "HTTP", Message: resp.Status}
	}

	result := &AskResult{}
	var (
		event    string
		data     strings.Builder
		sawDelta bool
	)
	// dispatch 处理一个已累积的事件；返回 done=true 表示问答正常结束
	dispatch := func() (bool, error) {
		payload := strings.TrimSpace(data.String())
		ev := event
		event, data = "", strings.Builder{}
		if payload == "" && ev == "" {
			return false, nil
		}
		switch ev {
		case "delta":
			var d struct {
				Content string `json:"content"`
			}
			if json.Unmarshal([]byte(payload), &d) == nil {
				result.Answer += d.Content
				sawDelta = sawDelta || d.Content != ""
			}
		case "citations":
			var ce struct {
				Items []Citation `json:"items"`
			}
			if json.Unmarshal([]byte(payload), &ce) == nil {
				result.Citations = append(result.Citations, ce.Items...)
			}
		case "error":
			var ee struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal([]byte(payload), &ee) == nil && ee.Code != "" {
				return false, &APIError{Status: resp.StatusCode, Code: ee.Code, Message: ee.Message}
			}
			return false, &APIError{Status: resp.StatusCode, Code: "SSE", Message: payload}
		case "done":
			return true, nil
		case "ping", "":
			// 心跳/无事件名：忽略
		}
		return false, nil
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // delta 的 data 行可能很长
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "":
			done, derr := dispatch()
			if derr != nil {
				return nil, derr
			}
			if done {
				return result, nil
			}
		}
	}
	// 流 EOF：冲刷最后一个事件（服务端异常断开时可能没有 done）
	done, derr := dispatch()
	if derr != nil {
		return nil, derr
	}
	if serr := sc.Err(); serr != nil && ctx.Err() == nil {
		return nil, fmt.Errorf("读取问答流: %w", serr)
	}
	if !done && ctx.Err() != nil {
		return result, fmt.Errorf("问答流中断（已返回部分答案，可能不完整）: %w", ctx.Err())
	}
	if !sawDelta && len(result.Citations) == 0 && !done {
		return nil, fmt.Errorf("问答流已结束但未收到任何内容")
	}
	return result, nil
}

// Ask 发起完整 RAG 问答：每次新建会话（残留可见可手动删除，v1 已知取舍）→ 消费 SSE → 拼装答案。
func (c *Client) Ask(ctx context.Context, kbID int64, question string) (*AskResult, error) {
	conv, err := c.CreateConversation(ctx, kbID)
	if err != nil {
		return nil, err
	}
	return c.askViaConversation(ctx, conv.ID, question)
}
