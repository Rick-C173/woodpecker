package qa

import (
	"context"
	"fmt"

	"woodpecker/internal/store"
	"woodpecker/internal/vector"
)

// DefaultQAService 默认问答服务实现
type DefaultQAService struct {
	retriever     Retriever
	generator     Generator
	knowledgeRepo *store.KnowledgeRepository
}

func NewQAService(
	retriever Retriever,
	generator Generator,
	knowledgeRepo *store.KnowledgeRepository,
) *DefaultQAService {
	return &DefaultQAService{
		retriever:     retriever,
		generator:     generator,
		knowledgeRepo: knowledgeRepo,
	}
}

func (s *DefaultQAService) Query(
	ctx context.Context,
	req *QueryRequest,
) (*QueryResponse, error) {
	// 1. 检索相关代码块
	results, err := s.retriever.Retrieve(ctx, req.Query, req.RepoOwner, req.RepoName)
	if err != nil {
		return nil, fmt.Errorf("retrieve: %w", err)
	}

	// 2. 生成回答
	answer, err := s.generator.Generate(ctx, req.Query, results)
	if err != nil {
		return nil, fmt.Errorf("generate answer: %w", err)
	}

	// 3. 构建来源引用
	sources := make([]*SourceRef, 0, len(results))
	for _, res := range results {
		chunk := res.Chunk
		sources = append(sources, &SourceRef{
			FilePath:   chunk.FilePath,
			StartLine:  chunk.StartLine,
			EndLine:    chunk.EndLine,
			Snippet:    truncateSnippet(chunk.Content, 200),
			SymbolName: chunk.SymbolName,
		})
	}

	// 4. 保存查询历史（可选）
	if s.knowledgeRepo != nil {
		_, _ = s.knowledgeRepo.AddQAHistory(ctx, req.RepoOwner, req.RepoName, req.Query, answer, sources)
	}

	return &QueryResponse{
		Answer:  answer,
		Sources: sources,
	}, nil
}

func truncateSnippet(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
