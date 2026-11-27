package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ClientResolver 从工具调用请求解析出本次调用的后端客户端。
//
// stdio 模式：token 在进程启动时固定，用 FixedClient 返回常量客户端；
// Streamable HTTP 模式（M17）：每个 HTTP 请求携带调用者自己的 Authorization header，
// 用 HTTPClientResolver 按 req.Extra.Header 构造 per-request 客户端——
// 团队共享部署下工具权限严格等于「发起该请求的用户」权限。
type ClientResolver func(req *mcp.CallToolRequest) (*Client, error)

// FixedClient 返回固定客户端的 resolver（stdio 单用户模式）。
func FixedClient(c *Client) ClientResolver {
	return func(*mcp.CallToolRequest) (*Client, error) { return c, nil }
}

// Register 把四个知识库工具注册到 MCP server。
// 工具描述面向 Agent 编写：说清用途、参数含义与行为边界（如 ask 的会话残留取舍）。
func Register(srv *mcp.Server, resolve ClientResolver) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_kbs",
		Description: "列出你有权限访问的知识库（含全员可见库）。调用其他工具前先用它确认 kb_id。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		c, err := resolve(req)
		if err != nil {
			return nil, nil, err
		}
		items, err := c.ListKBs(ctx)
		if err != nil {
			return nil, nil, err
		}
		var b strings.Builder
		if len(items) == 0 {
			b.WriteString("（无可见知识库）")
		}
		for _, k := range items {
			fmt.Fprintf(&b, "- [%d] %s（角色 %s）", k.ID, k.Name, k.MyRole)
			if k.Description != "" {
				fmt.Fprintf(&b, "：%s", k.Description)
			}
			b.WriteString("\n")
		}
		return textResult(b.String()), nil, nil
	})

	type searchIn struct {
		KBID  int64  `json:"kb_id" jsonschema:"知识库 ID（先调 list_kbs 获取）,required"`
		Query string `json:"query" jsonschema:"检索关键词（自然语言或代码标识符）,required"`
		TopK  int    `json:"top_k,omitempty" jsonschema:"返回条数上限，缺省 5，最大 100"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search",
		Description: "在指定知识库内做混合检索（向量 + 关键词），返回带 file_path:line 引用的知识切片。最快、不消耗 LLM，优先于 ask 使用。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, any, error) {
		c, err := resolve(req)
		if err != nil {
			return nil, nil, err
		}
		topK := in.TopK
		if topK == 0 {
			topK = 5
		}
		items, err := c.Search(ctx, in.KBID, in.Query, topK)
		if err != nil {
			return nil, nil, err
		}
		if len(items) == 0 {
			return textResult(fmt.Sprintf("知识库 %d 中未找到与 %q 相关的内容", in.KBID, in.Query)), nil, nil
		}
		var b strings.Builder
		for i, it := range items {
			fmt.Fprintf(&b, "[%d] %s\n%s\n\n", i+1, citeRef(it.FilePath, it.LineStart, it.LineEnd, it.HeadingPath), it.Content)
		}
		return textResult(strings.TrimRight(b.String(), "\n")), nil, nil
	})

	type askIn struct {
		KBID     int64  `json:"kb_id" jsonschema:"知识库 ID,required"`
		Question string `json:"question" jsonschema:"完整问题,required"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ask",
		Description: "对知识库发起 RAG 问答（消耗服务端 LLM 配额，较慢）。search 不够用时再用。注意：每次调用会新建一个会话（可在网页端查看/删除）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in askIn) (*mcp.CallToolResult, any, error) {
		c, err := resolve(req)
		if err != nil {
			return nil, nil, err
		}
		res, err := c.Ask(ctx, in.KBID, in.Question)
		if err != nil {
			return nil, nil, err
		}
		var b strings.Builder
		b.WriteString(res.Answer)
		if len(res.Citations) > 0 {
			b.WriteString("\n\n引用：")
			for _, ct := range res.Citations {
				fmt.Fprintf(&b, "\n[%d] %s", ct.Seq, citeRef(ct.FilePath, ct.LineStart, ct.LineEnd, ct.HeadingPath))
			}
		}
		return textResult(b.String()), nil, nil
	})

	type getDocIn struct {
		KBID  int64 `json:"kb_id" jsonschema:"知识库 ID,required"`
		DocID int64 `json:"doc_id" jsonschema:"文档 ID（search 结果中的 document_id）,required"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_document",
		Description: "查看文档元信息（类型/状态/文件数/来源地址），用于确认某文档是否已就绪、排查摄取失败原因。切片全文请用 search。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getDocIn) (*mcp.CallToolResult, any, error) {
		c, err := resolve(req)
		if err != nil {
			return nil, nil, err
		}
		d, err := c.GetDocument(ctx, in.KBID, in.DocID)
		if err != nil {
			return nil, nil, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "[%d] %s\n类型 %s · 状态 %s", d.ID, d.Name, d.Type, d.Status)
		if d.Stage != "" {
			fmt.Fprintf(&b, "（阶段 %s）", d.Stage)
		}
		fmt.Fprintf(&b, " · 文件数 %d", d.FileCount)
		if d.RepoURL != "" {
			fmt.Fprintf(&b, "\n仓库：%s", d.RepoURL)
		}
		if d.SourceURL != "" {
			fmt.Fprintf(&b, "\n来源：%s", d.SourceURL)
		}
		if d.Error != "" {
			fmt.Fprintf(&b, "\n失败原因：%s", d.Error)
		}
		return textResult(b.String()), nil, nil
	})
}

// textResult 单文本内容结果。
func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// citeRef 统一渲染引用标识（行号或标题路径，缺省显示占位说明）。
func citeRef(filePath *string, lineStart, lineEnd *int, headingPath *string) string {
	switch {
	case filePath != nil && lineStart != nil && lineEnd != nil && *lineEnd >= *lineStart:
		if *lineStart == *lineEnd {
			return fmt.Sprintf("%s:%d", *filePath, *lineStart)
		}
		return fmt.Sprintf("%s:%d-%d", *filePath, *lineStart, *lineEnd)
	case filePath != nil:
		return *filePath
	case headingPath != nil && *headingPath != "":
		return *headingPath
	default:
		return "（无路径引用）"
	}
}
