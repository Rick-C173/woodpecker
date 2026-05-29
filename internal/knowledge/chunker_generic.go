package knowledge

import (
	"fmt"
	"strings"
)

// GenericChunker 通用代码分块器（支持多种语言）
type GenericChunker struct {
	config  *Config
	chunker map[string]Chunker
}

func NewGenericChunker(cfg *Config) *GenericChunker {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	c := &GenericChunker{
		config:  cfg,
		chunker: make(map[string]Chunker),
	}

	c.chunker["go"] = NewGoChunker(cfg)
	c.chunker["python"] = &PythonChunker{config: cfg}
	c.chunker["javascript"] = &JSChunker{config: cfg}

	return c
}

func (c *GenericChunker) Chunk(content string, language string) ([]*CodeChunk, error) {
	if chunker, ok := c.chunker[language]; ok {
		return chunker.Chunk(content, language)
	}
	return c.chunkGeneric(content, language)
}

func (c *GenericChunker) chunkGeneric(content string, language string) ([]*CodeChunk, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var chunks []*CodeChunk

	if c.config.IncludeFile {
		chunks = append(chunks, &CodeChunk{
			ChunkType: ChunkTypeFile,
			StartLine: 1,
			EndLine:   len(lines),
			Content:   content,
		})
	}

	if len(lines) <= c.config.MaxChunkSize {
		return chunks, nil
	}

	numChunks := (len(lines) + c.config.MaxChunkSize - 1) / c.config.MaxChunkSize
	for i := 0; i < numChunks; i++ {
		start := i*c.config.MaxChunkSize + 1
		end := (i + 1) * c.config.MaxChunkSize
		if end > len(lines) {
			end = len(lines)
		}
		chunkContent := strings.Join(lines[start-1:end], "\n")
		chunks = append(chunks, &CodeChunk{
			ChunkType: ChunkTypeFile,
			StartLine: start,
			EndLine:   end,
			Content:   chunkContent,
		})
	}

	return chunks, nil
}

// PythonChunker Python 语言分块器
type PythonChunker struct {
	config *Config
}

func (c *PythonChunker) Chunk(content string, language string) ([]*CodeChunk, error) {
	if language != "python" {
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
	var inClass bool
	var startLine int
	var indentLevel int
	var funcName string
	var className string

	for i = 0; i < len(lines); i++ {
		line := lines[i]
		trimmedLine := strings.TrimSpace(line)

		if strings.HasPrefix(trimmedLine, "#") {
			continue
		}

		if !inFunc && !inClass {
			if strings.HasPrefix(trimmedLine, "def ") {
				inFunc = true
				startLine = i + 1
				indentLevel = getIndentLevel(line)
				funcName = extractPythonFuncName(trimmedLine)
				continue
			}
			if strings.HasPrefix(trimmedLine, "class ") {
				inClass = true
				startLine = i + 1
				indentLevel = getIndentLevel(line)
				className = extractPythonClassName(trimmedLine)
				continue
			}
		} else if inFunc {
			currentIndent := getIndentLevel(line)
			if currentIndent <= indentLevel && trimmedLine != "" && !strings.HasPrefix(trimmedLine, "#") {
				endLine := i
				chunks = append(chunks, c.createChunk(lines, startLine, endLine, ChunkTypeFunc, funcName))
				inFunc = false
				i--
			}
		} else if inClass {
			currentIndent := getIndentLevel(line)
			if currentIndent <= indentLevel && trimmedLine != "" && !strings.HasPrefix(trimmedLine, "#") {
				endLine := i
				chunks = append(chunks, c.createChunk(lines, startLine, endLine, ChunkTypeClass, className))
				inClass = false
				i--
			}
		}
	}

	if inFunc {
		chunks = append(chunks, c.createChunk(lines, startLine, len(lines), ChunkTypeFunc, funcName))
	}
	if inClass {
		chunks = append(chunks, c.createChunk(lines, startLine, len(lines), ChunkTypeClass, className))
	}

	return chunks, nil
}

func (c *PythonChunker) createChunk(lines []string, start, end int, typ ChunkType, symbolName string) *CodeChunk {
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

func extractPythonFuncName(line string) string {
	if idx := strings.Index(line, "("); idx != -1 {
		parts := strings.Fields(line[:idx])
		if len(parts) > 1 {
			return parts[1]
		}
	}
	return ""
}

func extractPythonClassName(line string) string {
	if idx := strings.Index(line, "("); idx != -1 {
		parts := strings.Fields(line[:idx])
		if len(parts) > 1 {
			return parts[1]
		}
	} else if idx := strings.Index(line, ":"); idx != -1 {
		parts := strings.Fields(line[:idx])
		if len(parts) > 1 {
			return parts[1]
		}
	}
	return ""
}

// JSChunker JavaScript/TypeScript 分块器
type JSChunker struct {
	config *Config
}

func (c *JSChunker) Chunk(content string, language string) ([]*CodeChunk, error) {
	if language != "javascript" {
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
	var inClass bool
	var startLine int
	var braceCount int
	var funcName string
	var className string

	for i = 0; i < len(lines); i++ {
		line := lines[i]
		trimmedLine := strings.TrimSpace(line)

		if strings.HasPrefix(trimmedLine, "//") || strings.HasPrefix(trimmedLine, "/*") || strings.HasPrefix(trimmedLine, "*") {
			continue
		}

		if !inFunc && !inClass {
			if strings.Contains(trimmedLine, "function ") {
				inFunc = true
				startLine = i + 1
				braceCount = strings.Count(line, "{") - strings.Count(line, "}")
				funcName = extractJSFuncName(trimmedLine)
			} else if strings.HasPrefix(trimmedLine, "class ") {
				inClass = true
				startLine = i + 1
				braceCount = strings.Count(line, "{") - strings.Count(line, "}")
				className = extractJSClassName(trimmedLine)
			}
		} else {
			braceCount += strings.Count(line, "{") - strings.Count(line, "}")
			if braceCount <= 0 {
				endLine := i + 1
				var typ ChunkType
				var name string
				if inFunc {
					typ = ChunkTypeFunc
					name = funcName
					inFunc = false
				} else {
					typ = ChunkTypeClass
					name = className
					inClass = false
				}
				chunks = append(chunks, c.createChunk(lines, startLine, endLine, typ, name))
			}
		}
	}

	if inFunc {
		chunks = append(chunks, c.createChunk(lines, startLine, len(lines), ChunkTypeFunc, funcName))
	}
	if inClass {
		chunks = append(chunks, c.createChunk(lines, startLine, len(lines), ChunkTypeClass, className))
	}

	return chunks, nil
}

func (c *JSChunker) createChunk(lines []string, start, end int, typ ChunkType, symbolName string) *CodeChunk {
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

func extractJSFuncName(line string) string {
	if idx := strings.Index(line, "function "); idx != -1 {
		rest := line[idx+9:]
		if idx2 := strings.Index(rest, "("); idx2 != -1 {
			name := strings.TrimSpace(rest[:idx2])
			if name != "" {
				return name
			}
		}
	}
	return ""
}

func extractJSClassName(line string) string {
	if idx := strings.Index(line, "class "); idx != -1 {
		rest := line[idx+6:]
		if idx2 := strings.IndexAny(rest, " {\n"); idx2 != -1 {
			name := strings.TrimSpace(rest[:idx2])
			if name != "" {
				return name
			}
		}
	}
	return ""
}
