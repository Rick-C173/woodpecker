package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"woodpecker/internal/vector"
)

// IndexStatus 索引状态
type IndexStatus string

const (
	IndexStatusPending  IndexStatus = "pending"
	IndexStatusIndexing IndexStatus = "indexing"
	IndexStatusSuccess  IndexStatus = "success"
	IndexStatusFailed   IndexStatus = "failed"
)

// IndexerConfig 索引器配置
type IndexerConfig struct {
	IncludePatterns []string // 包含的文件模式
	ExcludePatterns []string // 排除的文件模式
	BatchSize       int      // 批量大小
	MaxFileSize     int64    // 最大文件大小（字节）
}

// DefaultIndexerConfig 默认配置
func DefaultIndexerConfig() *IndexerConfig {
	return &IndexerConfig{
		IncludePatterns: []string{"*.go", "*.py", "*.js", "*.ts", "*.java", "*.cpp", "*.c", "*.rs", "*.rb", "*.php", "*.swift", "*.kt"},
		ExcludePatterns: []string{".git", "node_modules", "vendor", "target", "build", "dist"},
		BatchSize:       100,
		MaxFileSize:     10 * 1024 * 1024, // 10MB
	}
}

// Indexer 代码索引器
type Indexer struct {
	config        *IndexerConfig
	chunkConfig   *Config
	embedder      vector.Embedder
	store         vector.VectorStore
	repoCachePath string
}

func NewIndexer(
	indexerCfg *IndexerConfig,
	chunkCfg *Config,
	embedder vector.Embedder,
	store vector.VectorStore,
	repoCachePath string,
) *Indexer {
	if indexerCfg == nil {
		indexerCfg = DefaultIndexerConfig()
	}
	if chunkCfg == nil {
		chunkCfg = DefaultConfig()
	}
	if repoCachePath == "" {
		repoCachePath = "./repos"
	}

	return &Indexer{
		config:        indexerCfg,
		chunkConfig:   chunkCfg,
		embedder:      embedder,
		store:         store,
		repoCachePath: repoCachePath,
	}
}

// IndexRepo 索引一个仓库
func (i *Indexer) IndexRepo(ctx context.Context, repoOwner, repoName, repoPath string) error {
	repoPath = i.getRepoPath(repoOwner, repoName, repoPath)
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		return fmt.Errorf("repo not found: %s", repoPath)
	}

	fmt.Printf("开始索引仓库: %s/%s\n", repoOwner, repoName)

	var chunks []*vector.CodeChunk
	var vectors [][]float32
	chunker := NewGenericChunker(i.chunkConfig)

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			for _, exclude := range i.config.ExcludePatterns {
				if strings.Contains(path, exclude) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if info.Size() > i.config.MaxFileSize {
			return nil
		}

		relPath, err := filepath.Rel(repoPath, path)
		if err != nil {
			return nil
		}

		lang := DetectLanguage(path)
		if !i.shouldInclude(path) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		fileChunks, err := chunker.Chunk(string(content), lang)
		if err != nil {
			return nil
		}

		for _, chunk := range fileChunks {
			chunk.RepoOwner = repoOwner
			chunk.RepoName = repoName
			chunk.FilePath = relPath
			chunk.Language = lang

			chunk.ID = i.generateChunkID(repoOwner, repoName, relPath, chunk.StartLine, chunk.EndLine)

			textToEmbed := chunk.Content
			if chunk.SymbolName != "" {
				textToEmbed = fmt.Sprintf("%s: %s", chunk.SymbolName, chunk.Content)
			}

			vec, err := i.embedder.EmbedOne(ctx, textToEmbed)
			if err != nil {
				fmt.Printf("生成 Embedding 失败: %s - %v\n", chunk.ID, err)
				continue
			}

			vc := &vector.CodeChunk{
				ID:         chunk.ID,
				RepoOwner:  chunk.RepoOwner,
				RepoName:   chunk.RepoName,
				FilePath:   chunk.FilePath,
				Language:   chunk.Language,
				ChunkType:  string(chunk.ChunkType),
				StartLine:  chunk.StartLine,
				EndLine:    chunk.EndLine,
				SymbolName: chunk.SymbolName,
				Content:    chunk.Content,
			}

			chunks = append(chunks, vc)
			vectors = append(vectors, vec)

			if len(chunks) >= i.config.BatchSize {
				if err := i.store.UpsertBatch(ctx, chunks, vectors); err != nil {
					fmt.Printf("批量插入失败: %v\n", err)
				}
				chunks = chunks[:0]
				vectors = vectors[:0]
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	if len(chunks) > 0 {
		if err := i.store.UpsertBatch(ctx, chunks, vectors); err != nil {
			fmt.Printf("剩余批量插入失败: %v\n", err)
		}
	}

	fmt.Printf("索引完成: %s/%s\n", repoOwner, repoName)
	return nil
}

// IndexRepository 索引一个仓库（简化版，用于 API）
func (i *Indexer) IndexRepository(ctx context.Context, repoOwner, repoName string) error {
	return i.IndexRepo(ctx, repoOwner, repoName, "")
}

// IndexFile 索引单个文件
func (i *Indexer) IndexFile(ctx context.Context, repoOwner, repoName, filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	lang := DetectLanguage(filePath)
	chunker := NewGenericChunker(i.chunkConfig)

	fileChunks, err := chunker.Chunk(string(content), lang)
	if err != nil {
		return err
	}

	var chunks []*vector.CodeChunk
	var vectors [][]float32

	for _, chunk := range fileChunks {
		chunk.RepoOwner = repoOwner
		chunk.RepoName = repoName
		chunk.FilePath = filePath
		chunk.Language = lang

		chunk.ID = i.generateChunkID(repoOwner, repoName, filePath, chunk.StartLine, chunk.EndLine)

		textToEmbed := chunk.Content
		if chunk.SymbolName != "" {
			textToEmbed = fmt.Sprintf("%s: %s", chunk.SymbolName, chunk.Content)
		}

		vec, err := i.embedder.EmbedOne(ctx, textToEmbed)
		if err != nil {
			continue
		}

		vc := &vector.CodeChunk{
			ID:         chunk.ID,
			RepoOwner:  chunk.RepoOwner,
			RepoName:   chunk.RepoName,
			FilePath:   chunk.FilePath,
			Language:   chunk.Language,
			ChunkType:  string(chunk.ChunkType),
			StartLine:  chunk.StartLine,
			EndLine:    chunk.EndLine,
			SymbolName: chunk.SymbolName,
			Content:    chunk.Content,
		}

		chunks = append(chunks, vc)
		vectors = append(vectors, vec)
	}

	if len(chunks) > 0 {
		if err := i.store.UpsertBatch(ctx, chunks, vectors); err != nil {
			return err
		}
	}

	return nil
}

// DeleteRepo 删除仓库索引
func (i *Indexer) DeleteRepo(ctx context.Context, repoOwner, repoName string) error {
	return i.store.DeleteByFilter(ctx, &vector.Filter{
		RepoOwner: repoOwner,
		RepoName:  repoName,
	})
}

// CountChunks 统计仓库代码块数量
func (i *Indexer) CountChunks(ctx context.Context, repoOwner, repoName string) (int64, error) {
	return i.store.Count(ctx, &vector.Filter{
		RepoOwner: repoOwner,
		RepoName:  repoName,
	})
}

func (i *Indexer) getRepoPath(repoOwner, repoName, repoPath string) string {
	if repoPath != "" {
		return repoPath
	}
	return filepath.Join(i.repoCachePath, repoOwner, repoName)
}

func (i *Indexer) generateChunkID(repoOwner, repoName, filePath string, startLine, endLine int) string {
	key := fmt.Sprintf("%s/%s:%s:%d-%d", repoOwner, repoName, filePath, startLine, endLine)
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

func (i *Indexer) shouldInclude(path string) bool {
	ext := filepath.Ext(path)
	for _, p := range i.config.IncludePatterns {
		if matched, err := filepath.Match(p, filepath.Base(path)); err == nil && matched {
			return true
		}
		if strings.HasPrefix(p, "*.") && strings.EqualFold(ext, p[1:]) {
			return true
		}
	}
	return false
}

// IndexProgress 索引进度回调
type IndexProgress struct {
	TotalFiles    int
	IndexedFiles  int
	TotalChunks   int
	IndexedChunks int
	CurrentFile   string
}

// IndexWithProgress 带进度的索引
func (i *Indexer) IndexWithProgress(
	ctx context.Context,
	repoOwner, repoName, repoPath string,
	progressChan chan<- IndexProgress,
) error {
	defer close(progressChan)

	repoPath = i.getRepoPath(repoOwner, repoName, repoPath)
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		return fmt.Errorf("repo not found: %s", repoPath)
	}

	var files []string
	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			for _, exclude := range i.config.ExcludePatterns {
				if strings.Contains(path, exclude) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if info.Size() <= i.config.MaxFileSize && i.shouldInclude(path) {
			files = append(files, path)
		}
		return nil
	})

	totalFiles := len(files)
	chunker := NewGenericChunker(i.chunkConfig)
	var chunks []*vector.CodeChunk
	var vectors [][]float32
	totalChunks := 0
	indexedChunks := 0

	for idx, path := range files {
		relPath, _ := filepath.Rel(repoPath, path)
		lang := DetectLanguage(path)

		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		fileChunks, err := chunker.Chunk(string(content), lang)
		if err != nil {
			continue
		}

		totalChunks += len(fileChunks)

		for _, chunk := range fileChunks {
			chunk.RepoOwner = repoOwner
			chunk.RepoName = repoName
			chunk.FilePath = relPath
			chunk.Language = lang
			chunk.ID = i.generateChunkID(repoOwner, repoName, relPath, chunk.StartLine, chunk.EndLine)

			textToEmbed := chunk.Content
			if chunk.SymbolName != "" {
				textToEmbed = fmt.Sprintf("%s: %s", chunk.SymbolName, chunk.Content)
			}

			vec, err := i.embedder.EmbedOne(ctx, textToEmbed)
			if err != nil {
				continue
			}

			vc := &vector.CodeChunk{
				ID:         chunk.ID,
				RepoOwner:  chunk.RepoOwner,
				RepoName:   chunk.RepoName,
				FilePath:   chunk.FilePath,
				Language:   chunk.Language,
				ChunkType:  string(chunk.ChunkType),
				StartLine:  chunk.StartLine,
				EndLine:    chunk.EndLine,
				SymbolName: chunk.SymbolName,
				Content:    chunk.Content,
			}

			chunks = append(chunks, vc)
			vectors = append(vectors, vec)
			indexedChunks++

			if len(chunks) >= i.config.BatchSize {
				if err := i.store.UpsertBatch(ctx, chunks, vectors); err != nil {
					fmt.Printf("批量插入失败: %v\n", err)
				}
				chunks = chunks[:0]
				vectors = vectors[:0]
			}
		}

		if progressChan != nil {
			progressChan <- IndexProgress{
				TotalFiles:    totalFiles,
				IndexedFiles:  idx + 1,
				TotalChunks:   totalChunks,
				IndexedChunks: indexedChunks,
				CurrentFile:   relPath,
			}
		}
	}

	if len(chunks) > 0 {
		if err := i.store.UpsertBatch(ctx, chunks, vectors); err != nil {
			fmt.Printf("剩余批量插入失败: %v\n", err)
		}
	}

	return nil
}
