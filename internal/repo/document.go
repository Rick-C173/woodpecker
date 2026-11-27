package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
	AutoSync     bool // M14：定时同步开关（git 类型）
	HasWebhook   bool // M14：webhook secret 已配置（不暴露本体）
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
			AutoSync: d.AutoSync, HasWebhook: len(d.WebhookSecret) > 0,
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
// 用 pgconn.PgError 的结构化 ConstraintName 字段精确匹配索引名（审查 #4）：不依赖驱动
// 错误消息文本——换驱动/升级 pgx 都不会失配；唯一性判定仍校验 SQLSTATE 23505 双保险。
// gorm postgres driver 底层即 pgx，错误链上的 *pgconn.PgError 可被 errors.As 捕获。
func isTaskRunningConflict(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == sqlStateUniqueViolation && pgErr.ConstraintName == ingestTaskRunningIndex
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

// ListAutoSyncDocs 待定时同步的 Git 文档（M14）：auto_sync=true 且非处理中——
// 已就绪（周期增量）、失败（重试恢复）、待处理（首次入库后）均可被扫描入队 sync；
// status=processing（有运行中任务）的由 service 层 HasRunningTask 兜底跳过。
func (r *DocumentRepo) ListAutoSyncDocs(ctx context.Context) ([]model.Document, error) {
	var docs []model.Document
	err := r.dbx(ctx).
		Where("type = ? AND auto_sync = ? AND status <> ?", model.DocTypeGit, true, model.DocStatusProcessing).
		Order("id").Find(&docs).Error
	return docs, err
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

// FailStaleTasks 周期性僵尸回收：只看**心跳时间**，不看归属（验收回归 8-1 补强）。
//
// 与 FailZombieTasks 的分工（重要）：
//   - FailZombieTasks 只在**启动恢复**时跑，含「worker_id = 本实例 AND status = 2」段——启动瞬间
//     本实例还没有在跑的任务，这段是安全的；
//   - 本方法会被**周期性**调用（默认每 2 分钟），此时本实例可能正在处理任务，若带上归属段就会
//     把自己正在跑的任务杀掉，故条件里**绝不含 worker_id**，只按心跳（缺失时回落
//     started_at / created_at）判定。
//
// 覆盖场景：Pod 秒级重建——崩溃后新 Pod 立刻起来时心跳尚未过期，启动恢复看不到陈旧任务；
// 等心跳真正过期后由本方法回收，不必等到下一次重启。
//
// 返回受影响任务数，以及被波及的文档 ID（供 FailProcessingDocs 精确限定）。
func (r *DocumentRepo) FailStaleTasks(ctx context.Context, timeoutMinutes int) (tasks int64, docIDs []int64, err error) {
	if timeoutMinutes <= 0 {
		timeoutMinutes = defaultZombieTimeoutMinutes
	}
	cutoff := time.Now().Add(-time.Duration(timeoutMinutes) * time.Minute)
	err = r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		stale := func() *gorm.DB {
			return tx.Model(&model.IngestTask{}).
				Where("status = ? AND COALESCE(heartbeat_at, started_at, created_at) < ?",
					model.TaskStatusRunning, cutoff)
		}
		if perr := stale().Distinct("document_id").Pluck("document_id", &docIDs).Error; perr != nil {
			return perr
		}
		res := stale().Updates(map[string]any{
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

// TransitionTaskIfRunning 带守卫的任务状态流转（审查 #1）：
// 仅当任务仍处于 running 时应用 updates，返回是否落库成功。
//
// 为什么需要：sweeper（FailStaleTasks）可能已把心跳超时的 running 任务置 failed，
// 此时原 worker（只是心跳暂时写失败）完成处理后的收尾（成功/重排/最终失败）若用
// SaveTask 全量落库，会把 sweeper 的失败判定覆写回去；窗口期用户对 failed 文档发起
// retry 会因唯一索引放行而双跑。与 HeartbeatTask 同一守卫思想：任务先行终结则放弃覆写。
// false 不视为错误——任务行可能已随文档级联删除，或已被回收判定接管。
func (r *DocumentRepo) TransitionTaskIfRunning(ctx context.Context, taskID int64, updates map[string]any) (bool, error) {
	res := r.dbx(ctx).Model(&model.IngestTask{}).
		Where("id = ? AND status = ?", taskID, model.TaskStatusRunning).
		Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ---- 带 fencing 的写入（审查 #1 补强）----
//
// 背景：sweeper 可能把心跳超时的 running 任务置 failed，但原 worker 未必已死（心跳写失败、
// 长 GC、DB 抖动）。只守卫「任务状态收尾」不够——content_hash 若先落库，新任务会因 hash
// 相同而跳过该文件，而它的 chunks 可能没写，文档会永久缺块。
// 因此「文件元数据 + 分块」必须在同一事务内写入，且事务开头对任务行加 FOR UPDATE 锁校验
// 仍为 running：任务一旦被回收，后续所有写入立即拒绝（fencing），由新任务接管。

// IsTaskRunning 写入前的快速失败检查（非严格 fencing：检查与写入之间仍有窗口）。
// 严格的并发保护由 ReplaceFileChunksIfTaskRunning / ReplaceDocumentChunksIfTaskRunning
// 的事务内行锁提供，本方法只用于尽早停止无意义的远程抓取后续工作。
func (r *DocumentRepo) IsTaskRunning(ctx context.Context, taskID int64) (bool, error) {
	var t model.IngestTask
	err := r.dbx(ctx).Select("id", "status").Where("id = ?", taskID).Take(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return t.Status == model.TaskStatusRunning, nil
}

// lockTaskRunning 事务内对任务行加行锁并校验仍为 running。
// 返回 (false, nil) 表示任务不存在（已随文档级联删除）或已被回收——调用方必须放弃写入。
func lockTaskRunning(tx *gorm.DB, taskID int64) (bool, error) {
	var t model.IngestTask
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id", "status").
		Where("id = ?", taskID).
		Take(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return t.Status == model.TaskStatusRunning, nil
}

// upsertFileTx 事务内 upsert 文件清单（与 UpsertFile 同口径，复用调用方事务）。
func upsertFileTx(tx *gorm.DB, f *model.DocumentFile) error {
	return tx.Where("document_id = ? AND path = ?", f.DocumentID, f.Path).
		Assign(model.DocumentFile{
			Status: f.Status, Language: f.Language, Size: f.Size,
			ContentHash: f.ContentHash, Error: f.Error, UpdatedAt: time.Now(),
		}).
		FirstOrCreate(f).Error
}

// ReplaceFileChunksIfTaskRunning 带 fencing 的文件级替换（增量同步）：
// 事务内锁任务行校验 running → upsert 文件元数据 → 删该文件旧块 → 插新块。
// 四步同一事务，保证「hash 已更新但 blocks 未写」的中间态不会落库。
// 返回 applied=false 表示任务已被回收/终结，调用方必须中止且不得继续写其它文件。
func (r *DocumentRepo) ReplaceFileChunksIfTaskRunning(ctx context.Context, taskID, documentID int64, filePath string, file *model.DocumentFile, chunks []model.Chunk) (bool, error) {
	applied := false
	err := r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		ok, err := lockTaskRunning(tx, taskID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if file != nil {
			if err := upsertFileTx(tx, file); err != nil {
				return err
			}
			// chunks 的 file_id 由本事务 upsert 结果回填（buildChunks 阶段还没有文件 ID）
			for i := range chunks {
				id := file.ID
				chunks[i].FileID = &id
			}
		}
		if err := tx.Where("document_id = ? AND file_path = ?", documentID, filePath).
			Delete(&model.Chunk{}).Error; err != nil {
			return err
		}
		if err := insertChunks(tx, documentID, chunks); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

// ReplaceDocumentChunksIfTaskRunning 带 fencing 的整篇替换（全量摄取 / 重建索引）：
// 事务内锁任务行校验 running → upsert 全部文件元数据 → 整篇删块 → 批量插块。
func (r *DocumentRepo) ReplaceDocumentChunksIfTaskRunning(ctx context.Context, taskID, documentID int64, files []*model.DocumentFile, chunks []model.Chunk) (bool, error) {
	applied := false
	err := r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		ok, err := lockTaskRunning(tx, taskID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		idByPath := make(map[string]int64, len(files))
		for _, f := range files {
			if err := upsertFileTx(tx, f); err != nil {
				return err
			}
			idByPath[f.Path] = f.ID
		}
		for i := range chunks {
			if chunks[i].FilePath == nil {
				continue
			}
			if id, ok := idByPath[*chunks[i].FilePath]; ok {
				chunks[i].FileID = &id
			}
		}
		if err := tx.Where("document_id = ?", documentID).Delete(&model.Chunk{}).Error; err != nil {
			return err
		}
		if err := insertChunks(tx, documentID, chunks); err != nil {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

// UpdateDocumentIfTaskRunning 带 fencing 的文档状态写入（审查 #5）：
// 事务内锁任务行校验 running 后才更新文档。返回 applied=false 表示任务已被回收/删除，
// 调用方必须放弃处理——否则僵尸 worker 会把 sweeper 已置 failed 的文档覆盖回 processing，
// 而 Retry 只接受 failed、Sync 拒绝 processing，文档将永久卡死且无用户侧恢复入口。
func (r *DocumentRepo) UpdateDocumentIfTaskRunning(ctx context.Context, taskID, documentID int64, updates map[string]any) (bool, error) {
	applied := false
	err := r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		ok, err := lockTaskRunning(tx, taskID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		res := tx.Model(&model.Document{}).Where("id = ?", documentID).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		applied = res.RowsAffected > 0
		return nil
	})
	return applied, err
}

// MaintainFilesIfTaskRunning 带 fencing 的清单维护（审查 #5）：事务内锁任务行校验 running 后，
// 删除远端已不存在的文件记录及其分块、写入排除/超大文件标记。
// 这些操作原先在 fencing 事务之外，僵尸 worker 被回收后仍能删掉新任务刚写入的文件与分块。
func (r *DocumentRepo) MaintainFilesIfTaskRunning(ctx context.Context, taskID, documentID int64, removePaths []string, files []*model.DocumentFile) (bool, error) {
	applied := false
	err := r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		ok, err := lockTaskRunning(tx, taskID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if len(removePaths) > 0 {
			if err := tx.Where("document_id = ? AND path IN ?", documentID, removePaths).
				Delete(&model.DocumentFile{}).Error; err != nil {
				return err
			}
			// file_id 的级联只覆盖有 file_id 的块，历史/异常数据的块仍按路径兜底清理
			if err := tx.Where("document_id = ? AND file_path IN ?", documentID, removePaths).
				Delete(&model.Chunk{}).Error; err != nil {
				return err
			}
		}
		for _, f := range files {
			if err := upsertFileTx(tx, f); err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	return applied, err
}

// FinishTaskIfRunning 带 fencing 的收尾（审查 #5）：事务内锁任务行校验 running 后，
// 原子更新「任务终态 + 文档终态」。applied=false 时两者都不落库，避免出现
// 「任务 failed / 文档 ready」或旧 worker 覆盖新任务文档状态的矛盾终态。
func (r *DocumentRepo) FinishTaskIfRunning(ctx context.Context, taskID, documentID int64, taskUpdates, docUpdates map[string]any) (bool, error) {
	applied := false
	err := r.dbx(ctx).Transaction(func(tx *gorm.DB) error {
		ok, err := lockTaskRunning(tx, taskID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := tx.Model(&model.IngestTask{}).Where("id = ?", taskID).Updates(taskUpdates).Error; err != nil {
			return err
		}
		if len(docUpdates) > 0 {
			if err := tx.Model(&model.Document{}).Where("id = ?", documentID).Updates(docUpdates).Error; err != nil {
				return err
			}
		}
		applied = true
		return nil
	})
	return applied, err
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
