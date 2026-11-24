package model

import "time"

// 文档来源类型（documents.type）。
const (
	DocTypeGit    int16 = 1
	DocTypeUpload int16 = 2
	DocTypeURL    int16 = 3
)

// 文档状态机（数据库设计.md §5）：1 待处理 → 2 处理中 → 3 已就绪 / 4 失败。
// 禁止 4→2 直接流转；重试经 ingest_tasks 产生新任务。
const (
	DocStatusPending    int16 = 1
	DocStatusProcessing int16 = 2
	DocStatusReady      int16 = 3
	DocStatusFailed     int16 = 4
)

// 处理中阶段（documents.stage，仅 status=2）：1 解析 2 切块 3 向量化。
const (
	DocStageParse int16 = 1
	DocStageChunk int16 = 2
	DocStageEmbed int16 = 3
)

// 文件清单状态（document_files.status）。
const (
	FileStatusIndexed  int16 = 1
	FileStatusExcluded int16 = 2 // R1b 命中
	FileStatusFailed   int16 = 3
)

// 摄取任务类型（ingest_tasks.type）。
const (
	TaskTypeFull int16 = 1 // 全量摄取
	TaskTypeSync int16 = 2 // 增量同步
)

// 摄取任务状态（ingest_tasks.status）。
const (
	TaskStatusQueued  int16 = 1
	TaskStatusRunning int16 = 2
	TaskStatusSuccess int16 = 3
	TaskStatusFailed  int16 = 4
)

// Document 文档（知识源实例）。
type Document struct {
	ID            int64      `gorm:"primaryKey;column:id"`
	KBID          int64      `gorm:"column:kb_id"`
	Type          int16      `gorm:"column:type"`
	Name          string     `gorm:"column:name;size:500"`
	Status        int16      `gorm:"column:status;default:1"`
	Stage         *int16     `gorm:"column:stage"`
	Error         *string    `gorm:"column:error"`
	RepoURL       *string    `gorm:"column:repo_url;size:1000"`
	GitRef        *string    `gorm:"column:git_ref;size:200"`
	CredentialEnc []byte     `gorm:"column:credential_enc"`
	IgnoreRules   []string   `gorm:"column:ignore_rules;type:text[]"`
	LastCommit    *string    `gorm:"column:last_commit;size:40"`
	LastSyncedAt  *time.Time `gorm:"column:last_synced_at"`
	SourceURL     *string    `gorm:"column:source_url;size:2000"`
	FileCount     *int       `gorm:"column:file_count"`
	TotalSize     *int64     `gorm:"column:total_size"`
	CreatedBy     int64      `gorm:"column:created_by"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at"`
}

func (Document) TableName() string { return "documents" }

// DocumentFile 文档内文件清单（含"已排除"标记）。
type DocumentFile struct {
	ID          int64     `gorm:"primaryKey;column:id"`
	DocumentID  int64     `gorm:"column:document_id"`
	Path        string    `gorm:"column:path;size:1000"`
	Status      int16     `gorm:"column:status;default:1"`
	Language    *string   `gorm:"column:language;size:50"`
	Size        *int64    `gorm:"column:size"`
	ContentHash *string   `gorm:"column:content_hash;size:64"`
	Error       *string   `gorm:"column:error"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (DocumentFile) TableName() string { return "document_files" }

// IngestTask 摄取任务（全量/增量），支持重试计数。
//
// WorkerID 为任务归属实例（配置 WORKER_ID，迁移 000005）：入队时为 NULL，worker 开始处理时
// 写入自己的标识；重启恢复只清理「本实例」的残留，避免多实例互杀（审计 P1-8）。
type IngestTask struct {
	ID         int64      `gorm:"primaryKey;column:id"`
	DocumentID int64      `gorm:"column:document_id"`
	Type       int16      `gorm:"column:type"`
	Status     int16      `gorm:"column:status;default:1"`
	Retries    int        `gorm:"column:retries"`
	Error      *string    `gorm:"column:error"`
	Payload    []byte     `gorm:"column:payload;type:jsonb"`
	WorkerID   *string    `gorm:"column:worker_id;size:100"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	StartedAt  *time.Time `gorm:"column:started_at"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
}

func (IngestTask) TableName() string { return "ingest_tasks" }

// Chunk 文本分块（向量化入库，M5 检索使用）。
type Chunk struct {
	ID          int64     `gorm:"primaryKey;column:id"`
	DocumentID  int64     `gorm:"column:document_id"`
	KBID        int64     `gorm:"column:kb_id"`
	FileID      *int64    `gorm:"column:file_id"`
	Content     string    `gorm:"column:content"`
	ChunkIndex  int       `gorm:"column:chunk_index"`
	TokenCount  *int      `gorm:"column:token_count"`
	FilePath    *string   `gorm:"column:file_path;size:1000"`
	LineStart   *int      `gorm:"column:line_start"`
	LineEnd     *int      `gorm:"column:line_end"`
	HeadingPath *string   `gorm:"column:heading_path"`
	Embedding   []float32 `gorm:"column:embedding;type:vector(1024)"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (Chunk) TableName() string { return "chunks" }
