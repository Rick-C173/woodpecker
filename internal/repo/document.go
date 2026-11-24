package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"

	"woodpecker/internal/model"
)

// DocumentRow 文档列表行。
type DocumentRow struct {
	ID           int64
	Type         int16
	Name         string
	Status       int16
	Stage        *int16
	Error        *string
	SourceURL    *string
	RepoURL      *string
	GitRef       *string
	FileCount    *int
	TotalSize    *int64
	LastSyncedAt *time.Time
	CreatedBy    int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// DocumentListQuery 文档列表过滤。
type DocumentListQuery struct {
	KBID    int64
	Type    int16 // 0=不过滤
	Status  int16 // 0=不过滤
	Page    int
	PerPage int
}

// TaskRow 任务历史行。
type TaskRow struct {
	ID         int64
	Type       int16
	Status     int16
	Retries    int
	Error      *string
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// ErrDocumentNotFound 文档不存在（与全局 ErrNotFound 一致，便于 errors.Is 判断）。
var ErrDocumentNotFound = ErrNotFound

// ErrTaskRunning 该文档已有排队/运行中的摄取任务（唯一索引 uq_ingest_tasks_running 冲突）。
// 由 service 层映射为业务错误 DOC_TASK_RUNNING（409），故本包不引入 service 依赖。
var ErrTaskRunning = errors.New("该文档已有运行中的摄取任务")

// ingestTaskRunningIndex 「同文档运行中任务唯一」索引名（迁移 000006）。
const ingestTaskRunningIndex = "uq_ingest_tasks_running"

// zombieTaskError 重启恢复写入任务的错误摘要（FR-06：中断任务按失败处理）。
const zombieTaskError = "服务重启导致任务中断"

// defaultZombieTimeoutMinutes 心跳超时阈值兜底（与 config.DefaultWorkerZombieTimeoutMinutes 同值）。
// repo 层不引 config（保持仓储层无配置依赖），调用方传入 <=0 时用本值。
const defaultZombieTimeoutMinutes = 5

// sqlStateUniqueViolation PG 唯一键冲突的 SQLSTATE。
const sqlStateUniqueViolation = "23505"

type DocumentRepo struct{ db *gorm.DB }

func NewDocumentRepo(db *gorm.DB) *DocumentRepo { return &DocumentRepo{db: db} }

func (r *DocumentRepo) dbx(ctx context.Context) *gorm.DB { return r.db.WithContext(ctx) }

// ---- documents ----

func (r *DocumentRepo) CreateDocument(ctx context.Context, d *model.Document) error {
	return r.dbx(ctx).Create(d).Error
}

// CreateDocumentWithTask 同一事务内创建文档与其首个摄取任务（审计 P1-9）。
// 任一步失败整体回滚，不会留下「有文档没任务」的待处理僵尸文档。
func (r *DocumentRepo) CreateDocumentWithTask(ctx context.Context, d *model.Document, t *model.IngestTask) error {
	return r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(d).Error; err != nil {
			return err
		}
		t.DocumentID = d.ID
		return tx.Create(t).Error
	})
}

func (r *DocumentRepo) SaveDocument(ctx context.Context, d *model.Document) error {
	return r.dbx(ctx).Save(d).Error
}

func (r *DocumentRepo) GetDocumentByID(ctx context.Context, id int64) (*model.Document, error) {
	var d model.Document
	if err := r.dbx(ctx).First(&d, id).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &d, nil
}

// CountByKB 知识库文档数（R2 配额）。
func (r *DocumentRepo) CountByKB(ctx context.Context, kbID int64) (int64, error) {
	var n int64
	err := r.dbx(ctx).Model(&model.Document{}).Where("kb_id = ?", kbID).Count(&n).Error
	return n, err
}

// ListDocuments 分页列表。
func (r *DocumentRepo) ListDocuments(ctx context.Context, q DocumentListQuery) ([]DocumentRow, int64, error) {
	tx := r.dbx(ctx).Model(&model.Document{}).Where("kb_id = ?", q.KBID)
	if q.Type != 0 {
		tx = tx.Where("type = ?", q.Type)
	}
	if q.Status != 0 {
		tx = tx.Where("status = ?", q.Status)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PerPage < 1 || q.PerPage > 100 {
		q.PerPage = 20
	}
	var docs []model.Document
	if err := tx.Order("created_at DESC").Limit(q.PerPage).Offset((q.Page - 1) * q.PerPage).Find(&docs).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]DocumentRow, 0, len(docs))
	for _, d := range docs {
		rows = append(rows, DocumentRow{
			ID: d.ID, Type: d.Type, Name: d.Name, Status: d.Status, Stage: d.Stage,
			Error: d.Error, SourceURL: d.SourceURL, RepoURL: d.RepoURL, GitRef: d.GitRef,
			FileCount: d.FileCount, TotalSize: d.TotalSize, LastSyncedAt: d.LastSyncedAt,
			CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		})
	}
	return rows, total, nil
}

// DeleteDocument 删除文档（chunks/document_files/ingest_tasks 由外键级联）。
func (r *DocumentRepo) DeleteDocument(ctx context.Context, id int64) error {
	return r.dbx(ctx).Delete(&model.Document{}, id).Error
}

// ---- document_files ----

// UpsertFile 按 (document_id, path) 维护文件清单。
func (r *DocumentRepo) UpsertFile(ctx context.Context, f *model.DocumentFile) error {
	return r.dbx(ctx).
		Where("document_id = ? AND path = ?", f.DocumentID, f.Path).
		Assign(model.DocumentFile{Status: f.Status, Language: f.Language, Size: f.Size, ContentHash: f.ContentHash, Error: f.Error, UpdatedAt: time.Now()}).
		FirstOrCreate(f).Error
}

func (r *DocumentRepo) ListFiles(ctx context.Context, documentID int64) ([]model.DocumentFile, error) {
	var files []model.DocumentFile
	err := r.dbx(ctx).Where("document_id = ?", documentID).Order("path").Find(&files).Error
	return files, err
}

// GetFileByID 单个文件（增量同步删块定位用）。
func (r *DocumentRepo) GetFileByID(ctx context.Context, id int64) (*model.DocumentFile, error) {
	var f model.DocumentFile
	if err := r.dbx(ctx).First(&f, id).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &f, nil
}

// DeleteFilesByPath 删除指定文件记录（增量同步：远端已删除）。
func (r *DocumentRepo) DeleteFilesByPath(ctx context.Context, documentID int64, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return r.dbx(ctx).Where("document_id = ? AND path IN ?", documentID, paths).Delete(&model.DocumentFile{}).Error
}

// ---- chunks ----

// ReplaceDocumentChunks 整体替换文档全部分块（事务）：先删后插。
// 仅用于**全量摄取 / 重建索引**；增量同步必须按文件替换
// （DeleteChunksByFile + InsertChunks），否则未变更文件的分块会被整篇 DELETE 掉（P0-1）。
// embedding 为 pgvector 列，GORM 对 []float32 会写成 record 类型，故手写 INSERT 显式 ::vector 转换。
func (r *DocumentRepo) ReplaceDocumentChunks(ctx context.Context, documentID int64, chunks []model.Chunk) error {
	return r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("document_id = ?", documentID).Delete(&model.Chunk{}).Error; err != nil {
			return err
		}
		return insertChunks(tx, documentID, chunks)
	})
}

// InsertChunks 追加写入文档分块（事务内批量 INSERT）。
// 增量同步按文件替换的分块走这里；chunk_index 为**文件内序号**（见迁移 000003）。
func (r *DocumentRepo) InsertChunks(ctx context.Context, documentID int64, chunks []model.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	return r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		return insertChunks(tx, documentID, chunks)
	})
}

// insertChunks 事务内批量 INSERT（调用方负责事务与「先删旧块」）。
func insertChunks(tx *gorm.DB, documentID int64, chunks []model.Chunk) error {
	now := time.Now()
	for i := range chunks {
		c := &chunks[i]
		if c.DocumentID == 0 {
			c.DocumentID = documentID
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		c.UpdatedAt = now
		if err := tx.Exec(`INSERT INTO chunks
			(document_id, kb_id, file_id, content, chunk_index, token_count, file_path, line_start, line_end, heading_path, embedding, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::vector, ?, ?)`,
			c.DocumentID, c.KBID, c.FileID, c.Content, c.ChunkIndex, c.TokenCount, c.FilePath,
			c.LineStart, c.LineEnd, c.HeadingPath, pgvector.NewVector(c.Embedding), c.CreatedAt, c.UpdatedAt).Error; err != nil {
			return err
		}
	}
	return nil
}

// DeleteChunksByFile 删除某文件的所有分块（增量同步：文件被删/修改）。
func (r *DocumentRepo) DeleteChunksByFile(ctx context.Context, documentID int64, filePath string) error {
	return r.dbx(ctx).Where("document_id = ? AND file_path = ?", documentID, filePath).Delete(&model.Chunk{}).Error
}

// ---- ingest_tasks ----

// CreateTask 创建摄取任务。
// 同一文档最多一个排队/运行中任务由部分唯一索引 uq_ingest_tasks_running（迁移 000006）保证，
// 冲突时返回 ErrTaskRunning（审计 P1-10：应用层 HasRunningTask 的「先查后写」非原子，正确性靠 DB 兜底）。
func (r *DocumentRepo) CreateTask(ctx context.Context, t *model.IngestTask) error {
	err := r.dbx(ctx).Create(t).Error
	if err != nil && isTaskRunningConflict(err) {
		return ErrTaskRunning
	}
	return err
}

// isTaskRunningConflict 判定「同文档运行中任务唯一」冲突。
// 不用 pgconn 具体类型（避免为非直接依赖引入 import）：pgx 的 PgError 实现了
// `SQLState() string` 接口，可据此断言 SQLSTATE 23505，再用约束名排除其它唯一键。
func isTaskRunningConflict(err error) bool {
	if err == nil {
		return false
	}
	var carrier interface{ SQLState() string }
	if errors.As(err, &carrier) && carrier.SQLState() == sqlStateUniqueViolation {
		return strings.Contains(err.Error(), ingestTaskRunningIndex)
	}
	return false
}

// SaveTask 全字段落库，但**不覆盖 heartbeat_at**：该列只由 HeartbeatTask 维护。
// 否则 Process/fail/finishTask 里的 SaveTask 会用「从库里读出来的旧值（nil）」把它写回 NULL，
// 心跳刚建立的租约信息立刻丢失（验收回归 8-1）。
func (r *DocumentRepo) SaveTask(ctx context.Context, t *model.IngestTask) error {
	return r.dbx(ctx).Omit("heartbeat_at").Save(t).Error
}

func (r *DocumentRepo) GetTaskByID(ctx context.Context, id int64) (*model.IngestTask, error) {
	var t model.IngestTask
	if err := r.dbx(ctx).First(&t, id).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &t, nil
}

// HasRunningTask 该文档是否存在运行中任务（R3 幂等：排队/运行均算）。
func (r *DocumentRepo) HasRunningTask(ctx context.Context, documentID int64) (bool, error) {
	var n int64
	err := r.dbx(ctx).Model(&model.IngestTask{}).
		Where("document_id = ? AND status IN ?", documentID, []int16{model.TaskStatusQueued, model.TaskStatusRunning}).
		Count(&n).Error
	return n > 0, err
}

// ListTaskHistory 任务历史（新→旧）。
func (r *DocumentRepo) ListTaskHistory(ctx context.Context, documentID int64, limit int) ([]TaskRow, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var tasks []model.IngestTask
	err := r.dbx(ctx).Where("document_id = ?", documentID).
		Order("created_at DESC").Limit(limit).Find(&tasks).Error
	if err != nil {
		return nil, err
	}
	rows := make([]TaskRow, 0, len(tasks))
	for _, t := range tasks {
		rows = append(rows, TaskRow{
			ID: t.ID, Type: t.Type, Status: t.Status, Retries: t.Retries, Error: t.Error,
			CreatedAt: t.CreatedAt, StartedAt: t.StartedAt, FinishedAt: t.FinishedAt,
		})
	}
	return rows, nil
}

// FailZombieTasks 重启恢复：把**本实例遗留或心跳超时**的任务置失败（FR-06）。
//
// 多实例部署下不得全库 UPDATE——否则任一 worker 启动都会把其它实例正在处理的任务置失败（审计 P1-8）。
// 判定分两段（外层 OR，故显式分组）：
//   - worker_id = workerID 且 status = 2：本实例上次运行留下的运行中残留，可立即回收；
//   - status = 2 且心跳超时：心跳（缺失时回落 started_at / created_at）早于 cutoff 的运行中任务，
//     即跨实例僵尸——这是对「WORKER_ID 跨重启变化导致按归属匹配不到」的兜底
//     （验收回归 8-1：k8s 每次重建 Pod 主机名必变，旧实现会让任务永久卡在 running）。
//
// 为什么不清理排队态（status = 1）：CreateTask 入队时 worker_id 为空，只有 Process 开始处理才写归属；
// 而 asynq 队列是 Redis 持久化的，重启后这些排队任务仍会被重新投递执行——在 DB 里先把它们置失败，
// 会造成「用户看到失败、任务其实还在跑」的库/队列状态不一致，比原缺陷更隐蔽。故**无论归属如何**，
// 排队态一律交给 asynq 重投，DB 不动它（P1-8 收窄时定下的红线）。
//
// 返回受影响任务数，以及被波及的文档 ID（供 FailProcessingDocs 精确限定，不做全库 UPDATE）。
func (r *DocumentRepo) FailZombieTasks(ctx context.Context, workerID string, timeoutMinutes int) (tasks int64, docIDs []int64, err error) {
	if timeoutMinutes <= 0 {
		timeoutMinutes = defaultZombieTimeoutMinutes
	}
	// 在 Go 侧算 cutoff，避免在 SQL 里拼 interval（迁移只加列，不改查询方言）。
	cutoff := time.Now().Add(-time.Duration(timeoutMinutes) * time.Minute)
	err = r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		zombie := func() *gorm.DB {
			return tx.Model(&model.IngestTask{}).
				Where("(worker_id = ? AND status = ?) OR (status = ? AND COALESCE(heartbeat_at, started_at, created_at) < ?)",
					workerID, model.TaskStatusRunning,
					model.TaskStatusRunning, cutoff)
		}
		// 先取文档 ID（置失败后这些行不再命中 status 条件）
		if perr := zombie().Distinct("document_id").Pluck("document_id", &docIDs).Error; perr != nil {
			return perr
		}
		res := zombie().Updates(map[string]any{
			"status":      model.TaskStatusFailed,
			"error":       zombieTaskError,
			"finished_at": time.Now(),
		})
		tasks = res.RowsAffected
		return res.Error
	})
	if err != nil {
		return 0, nil, err
	}
	return tasks, docIDs, nil
}

// HeartbeatTask 刷新运行中任务的心跳时刻（验收回归 8-1）。
//
// 只在 status = 2 时更新：任务已终结（成功/失败/被清理）时心跳不得把状态"续命"，
// 返回 nil 即可——更新 0 行不是错误（任务行可能已随文档级联删除）。
func (r *DocumentRepo) HeartbeatTask(ctx context.Context, taskID int64) error {
	return r.dbx(ctx).Model(&model.IngestTask{}).
		Where("id = ? AND status = ?", taskID, model.TaskStatusRunning).
		Update("heartbeat_at", time.Now()).Error
}

// FailProcessingDocs 重启恢复：只把「本 worker 刚置失败的任务」对应的处理中文档置失败
// （配合 FailZombieTasks）。docIDs 为其返回值——绝不做 `WHERE status = 2` 的全库 UPDATE，
// 否则会误伤其它实例正在处理的文档（审计 P1-8）。
func (r *DocumentRepo) FailProcessingDocs(ctx context.Context, docIDs []int64) (int64, error) {
	if len(docIDs) == 0 {
		return 0, nil
	}
	res := r.dbx(ctx).Model(&model.Document{}).
		Where("id IN ?", docIDs).
		Where("status = ?", model.DocStatusProcessing).
		Updates(map[string]any{
			"status":     model.DocStatusFailed,
			"stage":      nil,
			"error":      zombieTaskError,
			"updated_at": time.Now(),
		})
	return res.RowsAffected, res.Error
}

// mapNotFound 统一 ErrNotFound 判定。
func mapNotFound(err error) error {
	if isNoRows(err) {
		return ErrNotFound
	}
	return err
}
