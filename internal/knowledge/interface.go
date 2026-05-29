package knowledge

import (
	"path/filepath"
	"strings"
)

// ChunkType 代码块类型
type ChunkType string

const (
	ChunkTypeFile    ChunkType = "file"
	ChunkTypeClass   ChunkType = "class"
	ChunkTypeStruct  ChunkType = "struct"
	ChunkTypeFunc    ChunkType = "function"
	ChunkTypeMethod  ChunkType = "method"
	ChunkTypeComment ChunkType = "comment"
)

// CodeChunk 知识块（与 vector 模块的 CodeChunk 一致，避免循环依赖）
type CodeChunk struct {
	ID         string
	RepoOwner  string
	RepoName   string
	FilePath   string
	Language   string
	ChunkType  ChunkType
	StartLine  int
	EndLine    int
	SymbolName string
	Content    string
}

// Chunker 代码分块器接口
type Chunker interface {
	// Chunk 将代码分成语义块
	Chunk(content string, language string) ([]*CodeChunk, error)
}

// Config 分块器配置
type Config struct {
	MaxChunkSize int  // 最大块大小（行）
	KeepOverlap  bool // 是否保留重叠
	IncludeFile  bool // 是否包含完整文件块
}

// DefaultConfig 默认配置
func DefaultConfig() *Config {
	return &Config{
		MaxChunkSize: 50,
		KeepOverlap:  true,
		IncludeFile:  true,
	}
}

// detectLanguage 从文件扩展名识别语言
func DetectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".jsx", ".ts", ".tsx":
		return "javascript"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".cpp", ".hpp", ".cc":
		return "cpp"
	case ".c", ".h":
		return "c"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	default:
		return "text"
	}
}
