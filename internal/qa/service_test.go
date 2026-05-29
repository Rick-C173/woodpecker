package qa

import (
	"context"
	"testing"

	"woodpecker/internal/engine/llm"
	"woodpecker/internal/vector"
)

// 模拟 Retriever
type mockRetriever struct{}

func (m *mockRetriever) Retrieve(ctx context.Context, query string, repoOwner, repoName string) ([]*vector.SearchResult, error) {
	// 返回一些模拟的搜索结果
	return []*vector.SearchResult{
		{
			Chunk: &vector.CodeChunk{
				ID:         "1",
				RepoOwner:  "test",
				RepoName:   "repo",
				FilePath:   "main.go",
				Language:   "Go",
				ChunkType:  "function",
				StartLine:  10,
				EndLine:    20,
				SymbolName: "main",
				Content:    "func main() {\n    fmt.Println(\"Hello World\")\n}",
			},
			Score: 0.95,
		},
	}, nil
}

// 模拟 Generator
type mockGenerator struct{}

func (m *mockGenerator) Generate(ctx context.Context, query string, results []*vector.SearchResult) (string, error) {
	return "这是一个模拟的回答，基于提供的代码片段。", nil
}

func TestRAGPromptBuilder_Build(t *testing.T) {
	pb, err := NewRAGPromptBuilder()
	if err != nil {
		t.Fatalf("failed to create prompt builder: %v", err)
	}

	query := "项目的入口函数是什么？"
	results := []*vector.SearchResult{
		{
			Chunk: &vector.CodeChunk{
				FilePath:   "main.go",
				StartLine:  1,
				EndLine:    10,
				Language:   "Go",
				ChunkType:  "function",
				SymbolName: "main",
				Content:    "func main() {\n\tprintln(\"hello\")\n}",
			},
			Score: 0.9,
		},
	}

	prompt, err := pb.Build(query, results)
	if err != nil {
		t.Fatalf("failed to build prompt: %v", err)
	}

	// 验证 prompt 包含必要信息
	checks := []string{
		"项目的入口函数是什么？",
		"main.go",
		"func main",
		"代码片段",
	}

	for _, check := range checks {
		if !contains(prompt, check) {
			t.Errorf("prompt missing: %q", check)
		}
	}

	t.Logf("Prompt built successfully, length: %d", len(prompt))
}

func TestDefaultQAService_Query(t *testing.T) {
	retriever := &mockRetriever{}
	generator := &mockGenerator{}

	qaService := NewQAService(retriever, generator, nil)

	req := &QueryRequest{
		Query:     "项目的入口函数是什么？",
		RepoOwner: "test",
		RepoName:  "repo",
	}

	ctx := context.Background()
	resp, err := qaService.Query(ctx, req)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}

	if resp.Answer == "" {
		t.Error("expected non-empty answer")
	}

	if len(resp.Sources) == 0 {
		t.Error("expected at least one source")
	}

	t.Logf("Query succeeded, answer: %s", resp.Answer)
	t.Logf("Sources count: %d", len(resp.Sources))
}

func TestLLMGenerator_Generate(t *testing.T) {
	llmClient := llm.NewMockClient()
	promptBuilder, err := NewRAGPromptBuilder()
	if err != nil {
		t.Fatalf("failed to create prompt builder: %v", err)
	}

	generator := NewLLMGenerator(llmClient, promptBuilder)

	ctx := context.Background()
	query := "测试查询"
	results := []*vector.SearchResult{}

	answer, err := generator.Generate(ctx, query, results)
	if err != nil {
		t.Fatalf("failed to generate answer: %v", err)
	}

	if answer == "" {
		t.Error("expected non-empty answer")
	}

	t.Logf("Generated answer: %s", answer)
}

func TestVectorRetriever_Retrieve(t *testing.T) {
	// 这个测试需要实际的向量存储和嵌入生成器
	// 在实际环境中，我们可以使用模拟实现
	t.Skip("skipping integration test; requires vector DB and embedder")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[0:len(s)] != "" && indexOf(s, substr) != -1
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
