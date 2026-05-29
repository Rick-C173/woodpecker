package qa

import (
	"context"

	"woodpecker/internal/vector"
)

// QueryRequest 查询请求
type QueryRequest struct {
	Query     string `json:"query"`
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
}

// QueryResponse 查询响应
type QueryResponse struct {
	Answer  string       `json:"answer"`
	Sources []*SourceRef `json:"sources"`
}

// SourceRef 来源引用
type SourceRef struct {
	FilePath   string `json:"file_path"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	Snippet    string `json:"snippet"`
	SymbolName string `json:"symbol_name,omitempty"`
}

// QAService 问答服务接口
type QAService interface {
	// Query 执行自然语言查询
	Query(ctx context.Context, req *QueryRequest) (*QueryResponse, error)
}

// Retriever 检索器接口
type Retriever interface {
	// Retrieve 检索相关代码块
	Retrieve(ctx context.Context, query string, repoOwner, repoName string) ([]*vector.SearchResult, error)
}

// Generator 回答生成器接口
type Generator interface {
	// Generate 基于检索结果生成回答
	Generate(ctx context.Context, query string, results []*vector.SearchResult) (string, error)
}
