package knowledge

import (
	"fmt"
	"strings"
)

// GoChunker Go 语言代码分块器
type GoChunker struct {
	config *Config
}

func NewGoChunker(cfg *Config) *GoChunker {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &GoChunker{config: cfg}
}

func (c *GoChunker) Chunk(content string, language string) ([]*CodeChunk, error) {
	if language != "go" {
		return nil, fmt.Errorf("unsupported language: %s", language)
	}

	var chunks []*CodeChunk
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	if c.config.IncludeFile {
		chunks = append(chunks, &CodeChunk{
			ChunkType: ChunkTypeFile,
			StartLine: 1,
			EndLine:   len(lines),
			Content:   content,
		})
	}

	var i int
	var inFunc bool
	var inStruct bool
	var inCommentBlock bool
	var startLine int
	var indentLevel int
	var funcName string
	var structName string

	for i = 0; i < len(lines); i++ {
		line := lines[i]
		trimmedLine := strings.TrimSpace(line)

		if strings.HasPrefix(trimmedLine, "/*") {
			inCommentBlock = true
		}
		if strings.HasSuffix(trimmedLine, "*/") {
			inCommentBlock = false
			continue
		}
		if inCommentBlock {
			continue
		}

		if !inFunc && !inStruct {
			if strings.HasPrefix(trimmedLine, "func ") {
				inFunc = true
				startLine = i + 1
				indentLevel = getIndentLevel(line)
				funcName = extractFuncName(trimmedLine)
				continue
			}
			if strings.HasPrefix(trimmedLine, "type ") && strings.Contains(trimmedLine, "struct") {
				inStruct = true
				startLine = i + 1
				indentLevel = getIndentLevel(line)
				structName = extractStructName(trimmedLine)
				continue
			}
		} else if inFunc {
			currentIndent := getIndentLevel(line)
			if currentIndent <= indentLevel && (trimmedLine == "}" || strings.HasPrefix(trimmedLine, "}")) {
				endLine := i + 1
				chunks = append(chunks, c.createChunk(lines, startLine, endLine, ChunkTypeFunc, funcName))
				inFunc = false
			}
		} else if inStruct {
			currentIndent := getIndentLevel(line)
			if currentIndent <= indentLevel && (trimmedLine == "}" || strings.HasPrefix(trimmedLine, "}")) {
				endLine := i + 1
				chunks = append(chunks, c.createChunk(lines, startLine, endLine, ChunkTypeStruct, structName))
				inStruct = false
			}
		}
	}

	if inFunc {
		chunks = append(chunks, c.createChunk(lines, startLine, len(lines), ChunkTypeFunc, funcName))
	}
	if inStruct {
		chunks = append(chunks, c.createChunk(lines, startLine, len(lines), ChunkTypeStruct, structName))
	}

	return chunks, nil
}

func (c *GoChunker) createChunk(lines []string, start, end int, typ ChunkType, symbolName string) *CodeChunk {
	if end <= 0 {
		end = len(lines)
	}
	if start < 1 {
		start = 1
	}
	if start > end {
		start, end = end, start
	}
	if start-1 >= len(lines) {
		start = len(lines)
		end = len(lines)
	}
	if end > len(lines) {
		end = len(lines)
	}

	content := strings.Join(lines[start-1:end], "\n")
	return &CodeChunk{
		ChunkType:  typ,
		StartLine:  start,
		EndLine:    end,
		SymbolName: symbolName,
		Content:    content,
	}
}

func getIndentLevel(line string) int {
	level := 0
	for _, r := range line {
		if r == ' ' {
			level++
		} else if r == '\t' {
			level += 4
		} else {
			break
		}
	}
	return level
}

func extractFuncName(line string) string {
	parts := strings.Fields(line)
	for i, p := range parts {
		if p == "func" && i+1 < len(parts) {
			namePart := parts[i+1]
			if strings.HasPrefix(namePart, "(") {
				return ""
			}
			if idx := strings.Index(namePart, "("); idx != -1 {
				return namePart[:idx]
			}
			return namePart
		}
	}
	return ""
}

func extractStructName(line string) string {
	parts := strings.Fields(line)
	for i, p := range parts {
		if p == "type" && i+2 < len(parts) && (parts[i+2] == "struct" || strings.HasPrefix(parts[i+2], "struct")) {
			return parts[i+1]
		}
	}
	return ""
}
