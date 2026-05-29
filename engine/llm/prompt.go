package llm

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"woodpecker/po"
)

// PromptBuilder 构建 LLM 提示词
type PromptBuilder struct {
	tmpl *template.Template
}

// NewPromptBuilder 创建默认的 Prompt 构建器
func NewPromptBuilder() (*PromptBuilder, error) {
	tmpl, err := template.New("review").Parse(reviewPromptTemplate)
	if err != nil {
		return nil, err
	}
	return &PromptBuilder{tmpl: tmpl}, nil
}

// Build 根据请求构建完整的 prompt 字符串
func (pb *PromptBuilder) Build(req ReviewRequest) (string, error) {
	var buf bytes.Buffer

	data := promptData{
		Language: req.Language,
		Context:  req.Context,
		Files:    make([]filePromptData, 0, len(req.FileDiffs)),
	}

	for _, fd := range req.FileDiffs {
		fpd := filePromptData{
			Path:     fd.NewPath,
			DiffText: renderDiffText(fd),
		}
		data.Files = append(data.Files, fpd)
	}

	if err := pb.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// promptData 模板数据
type promptData struct {
	Language string
	Context  string
	Files    []filePromptData
}

type filePromptData struct {
	Path     string
	DiffText string
}

// renderDiffText 将 FileDiff 渲染为统一的 diff 文本
func renderDiffText(fd po.FileDiff) string {
	var b strings.Builder
	b.WriteString("--- a/" + fd.OldPath + "\n")
	b.WriteString("+++ b/" + fd.NewPath + "\n")

	for _, hunk := range fd.Hunks {
		b.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n",
			hunk.OldStart, hunk.OldLines,
			hunk.NewStart, hunk.NewLines))

		for _, line := range hunk.Lines {
			b.WriteString(line.Type + line.Content + "\n")
		}
	}

	return b.String()
}

// reviewPromptTemplate 审查提示词模板
const reviewPromptTemplate = `你是一位资深的 {{.Language}} 代码审查专家。请仔细审查以下代码变更，找出潜在问题。

{{if .Context}}
## 额外上下文
{{.Context}}
{{end}}

## 审查要求

请按以下维度进行审查：
1. **bug** - 逻辑错误、空指针、资源泄漏、并发问题
2. **security** - SQL 注入、XSS、敏感信息硬编码、权限问题
3. **performance** - 算法复杂度、内存分配、N+1 查询、重复计算
4. **style** - 命名规范、代码风格、注释质量
5. **suggestion** - 可维护性、设计模式、最佳实践建议

## 输出格式

请严格按以下 JSON 格式输出（不要包含 markdown 代码块标记）：

{
  "comments": [
    {
      "file_path": "文件路径",
      "line": 行号,
      "category": "bug|security|performance|style|suggestion",
      "severity": "critical|warning|info",
      "message": "具体问题描述，包含原因和后果",
      "suggestion": "建议的修复代码（可选）",
      "confidence": 0.95
    }
  ],
  "summary": "整体评价：变更质量如何，主要关注点是什么"
}

## 待审查代码

{{range .Files}}
### {{.Path}}
` + "```diff" + `
{{.DiffText}}
` + "```" + `

{{end}}

请只输出 JSON，不要其他解释。`
