package qa

import (
	"context"
	"fmt"

	"woodpecker/internal/engine/llm"
	"woodpecker/internal/vector"
)

// LLMGenerator 基于 LLM 的回答生成器
type LLMGenerator struct {
	llmClient     llm.LlmClient
	promptBuilder *RAGPromptBuilder
}

func NewLLMGenerator(
	llmClient llm.LlmClient,
	promptBuilder *RAGPromptBuilder,
) *LLMGenerator {
	return &LLMGenerator{
		llmClient:     llmClient,
		promptBuilder: promptBuilder,
	}
}

func (g *LLMGenerator) Generate(
	ctx context.Context,
	query string,
	results []*vector.SearchResult,
) (string, error) {
	// 构建 RAG 提示词
	prompt, err := g.promptBuilder.Build(query, results)
	if err != nil {
		return "", fmt.Errorf("build prompt: %w", err)
	}

	// 调用 LLM 生成回答
	answer, err := g.llmClient.Chat(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("call LLM: %w", err)
	}

	return answer, nil
}
