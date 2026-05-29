package qa

import (
	"bytes"
	"text/template"

	"woodpecker/internal/vector"
)

// RAGPromptBuilder RAG 提示词构建器
type RAGPromptBuilder struct {
	tmpl *template.Template
}

func NewRAGPromptBuilder() (*RAGPromptBuilder, error) {
	tmpl, err := template.New("rag").Parse(ragPromptTemplate)
	if err != nil {
		return nil, err
	}
	return &RAGPromptBuilder{tmpl: tmpl}, nil
}

// Build 构建 RAG 提示词
func (pb *RAGPromptBuilder) Build(query string, results []*vector.SearchResult) (string, error) {
	var buf bytes.Buffer

	// 准备模板数据
	data := ragPromptData{
		Query:   query,
		Context: make([]contextItem, 0, len(results)),
	}

	for _, res := range results {
		chunk := res.Chunk
		item := contextItem{
			FilePath:   chunk.FilePath,
			StartLine:  chunk.StartLine,
			EndLine:    chunk.EndLine,
			Language:   chunk.Language,
			ChunkType:  chunk.ChunkType,
			SymbolName: chunk.SymbolName,
			Content:    chunk.Content,
			Score:      res.Score,
		}
		data.Context = append(data.Context, item)
	}

	if err := pb.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

type ragPromptData struct {
	Query   string
	Context []contextItem
}

type contextItem struct {
	FilePath   string
	StartLine  int
	EndLine    int
	Language   string
	ChunkType  string
	SymbolName string
	Content    string
	Score      float64
}

// ragPromptTemplate RAG 提示词模板
const ragPromptTemplate = `你是一位代码专家，擅长基于代码库回答问题。

## 用户问题
{{.Query}}

## 参考代码片段
{{range .Context}}
---
### {{.FilePath}} (行 {{.StartLine}}-{{.EndLine}})
类型: {{.ChunkType}}{{if .SymbolName}}
符号: {{.SymbolName}}{{end}}
语言: {{.Language}}
相关度: {{printf "%.2f" .Score}}

` + "```{{.Language}}" + `
{{.Content}}
` + "```" + `
{{end}}

## 回答要求
1. 基于提供的代码片段回答问题，不要编造信息
2. 如果没有相关代码，明确告知用户
3. 回答要准确、具体，尽可能引用代码中的关键部分
4. 提及相关的文件路径和行号，方便用户查找
5. 保持专业、清晰的表达

请直接回答问题，不要重复上述要求。`
