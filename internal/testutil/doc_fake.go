package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	"woodpecker/internal/model"
	"woodpecker/internal/repo"
)

// FakeDocRepo 文档存储替身：内存实现，支持错误注入（service 与 worker 共用）。
type FakeDocRepo struct {
	mu         sync.Mutex
	docs       map[int64]*model.Document
	files      map[int64][]model.DocumentFile // docID → files
	chunks     map[int64][]model.Chunk        // docID → chunks（真实内存实现，见 P0-1 盲区）
	tasks      map[int64]*model.IngestTask
	nextDoc    int64
	nextFile   int64
	nextTaskID int64
	DeletedDoc []int64

	ErrOnCreateDoc  error
	ErrOnCreateTask error
	ErrOnSaveDoc    error
	ErrOnSaveTask   error
	ErrOnGetDoc     error
	ErrOnCount      error
	ErrOnList       error
	ErrOnDelete     error
	ErrOnUpsertFile error
	ErrOnHasRunning error
	ErrOnHistory    error
	ErrOnReplace    error
	ErrOnInsert     error
}

func NewFakeDocs() *FakeDocRepo {
	return &FakeDocRepo{
		docs:       map[int64]*model.Document{},
		files:      map[int64][]model.DocumentFile{},
		chunks:     map[int64][]model.Chunk{},
		tasks:      map[int64]*model.IngestTask{},
		nextDoc:    1,
		nextFile:   1,
		nextTaskID: 1,
	}
}

func (f *FakeDocRepo) CreateDocument(_ context.Context, d *model.Document) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnCreateDoc != nil {
		return f.ErrOnCreateDoc
	}
	d.ID = f.nextDoc
	f.nextDoc++
	now := time.Now()
	d.CreatedAt, d.UpdatedAt = now, now
	cp := *d
	f.docs[d.ID] = &cp
	return nil
}

// CreateDocumentWithTask 同事务语义：建任务失败则回滚文档（与 repo 实现一致）。
func (f *FakeDocRepo) CreateDocumentWithTask(ctx context.Context, d *model.Document, t *model.IngestTask) error {
	if err := f.CreateDocument(ctx, d); err != nil {
		return err
	}
	t.DocumentID = d.ID
	if err := f.CreateTask(ctx, t); err != nil {
		_ = f.DeleteDocument(ctx, d.ID)
		return err
	}
	return nil
}

func (f *FakeDocRepo) SaveDocument(_ context.Context, d *model.Document) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnSaveDoc != nil {
		return f.ErrOnSaveDoc
	}
	if _, ok := f.docs[d.ID]; !ok {
		return repo.ErrNotFound
	}
	cp := *d
	f.docs[d.ID] = &cp
	return nil
}

func (f *FakeDocRepo) GetDocumentByID(_ context.Context, id int64) (*model.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnGetDoc != nil {
		return nil, f.ErrOnGetDoc
	}
	d, ok := f.docs[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *d
	return &cp, nil
}

func (f *FakeDocRepo) CountByKB(_ context.Context, kbID int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnCount != nil {
		return 0, f.ErrOnCount
	}
	var n int64
	for _, d := range f.docs {
		if d.KBID == kbID {
			n++
		}
	}
	return n, nil
}

func (f *FakeDocRepo) ListDocuments(_ context.Context, q repo.DocumentListQuery) ([]repo.DocumentRow, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnList != nil {
		return nil, 0, f.ErrOnList
	}
	var rows []repo.DocumentRow
	for _, d := range f.docs {
		if d.KBID != q.KBID {
			continue
		}
		if q.Type != 0 && d.Type != q.Type {
			continue
		}
		if q.Status != 0 && d.Status != q.Status {
			continue
		}
		rows = append(rows, repo.DocumentRow{
			ID: d.ID, Type: d.Type, Name: d.Name, Status: d.Status, Stage: d.Stage,
			Error: d.Error, SourceURL: d.SourceURL, RepoURL: d.RepoURL, GitRef: d.GitRef,
			FileCount: d.FileCount, TotalSize: d.TotalSize, LastSyncedAt: d.LastSyncedAt,
			CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		})
	}
	return rows, int64(len(rows)), nil
}

// DeleteDocument 删除文档，并级联删除文件清单、分块与任务。
// 与真库同口径（ingest_tasks.document_id / document_files / chunks 均 ON DELETE CASCADE）：
// 不级联时「删除处理中文档」的残留任务行为与生产不一致。
func (f *FakeDocRepo) DeleteDocument(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnDelete != nil {
		return f.ErrOnDelete
	}
	if _, ok := f.docs[id]; !ok {
		return repo.ErrNotFound
	}
	delete(f.docs, id)
	delete(f.files, id)
	delete(f.chunks, id)
	for taskID, t := range f.tasks {
		if t.DocumentID == id {
			delete(f.tasks, taskID)
		}
	}
	f.DeletedDoc = append(f.DeletedDoc, id)
	return nil
}

func (f *FakeDocRepo) UpsertFile(_ context.Context, file *model.DocumentFile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upsertFileLocked(file)
}

// upsertFileLocked 调用方须已持有 f.mu（带 fencing 的写入需要元数据与分块在同一临界区内生效）。
func (f *FakeDocRepo) upsertFileLocked(file *model.DocumentFile) error {
	if f.ErrOnUpsertFile != nil {
		return f.ErrOnUpsertFile
	}
	list := f.files[file.DocumentID]
	for i := range list {
		if list[i].Path == file.Path {
			if file.ID == 0 {
				file.ID = list[i].ID
			}
			list[i] = *file
			return nil
		}
	}
	if file.ID == 0 {
		file.ID = f.nextFile
		f.nextFile++
	}
	file.UpdatedAt = time.Now()
	f.files[file.DocumentID] = append(list, *file)
	return nil
}

func (f *FakeDocRepo) ListFiles(_ context.Context, documentID int64) ([]model.DocumentFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.DocumentFile, 0, len(f.files[documentID]))
	out = append(out, f.files[documentID]...)
	return out, nil
}

func (f *FakeDocRepo) DeleteFilesByPath(_ context.Context, documentID int64, paths []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	del := map[string]bool{}
	for _, p := range paths {
		del[p] = true
	}
	var kept []model.DocumentFile
	for _, file := range f.files[documentID] {
		if !del[file.Path] {
			kept = append(kept, file)
		}
	}
	f.files[documentID] = kept
	return nil
}

func (f *FakeDocRepo) ReplaceDocumentChunks(_ context.Context, documentID int64, chunks []model.Chunk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnReplace != nil {
		return f.ErrOnReplace
	}
	// 真实语义：先整篇删除，再插入传入的块
	stored := make([]model.Chunk, 0, len(chunks))
	for _, c := range chunks {
		if c.DocumentID == 0 {
			c.DocumentID = documentID
		}
		stored = append(stored, c)
	}
	f.chunks[documentID] = stored
	return nil
}

// InsertChunks 追加分块，并按 UNIQUE(document_id, file_path, chunk_index) 校验冲突。
// 与真库同口径：增量同步若仍用「全篇序号」会立刻暴露冲突（P0-1 的坑）。
func (f *FakeDocRepo) InsertChunks(_ context.Context, documentID int64, chunks []model.Chunk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.insertChunksLocked(documentID, chunks)
}

// insertChunksLocked 调用方须已持有 f.mu。
func (f *FakeDocRepo) insertChunksLocked(documentID int64, chunks []model.Chunk) error {
	if f.ErrOnInsert != nil {
		return f.ErrOnInsert
	}
	list := f.chunks[documentID]
	seen := make(map[string]bool, len(list)+len(chunks))
	for _, c := range list {
		seen[chunkKey(c)] = true
	}
	for _, c := range chunks {
		if c.DocumentID == 0 {
			c.DocumentID = documentID
		}
		k := chunkKey(c)
		if seen[k] {
			return fmt.Errorf("chunks 唯一键冲突: document_id=%d file_path=%s chunk_index=%d",
				documentID, derefStr(c.FilePath), c.ChunkIndex)
		}
		seen[k] = true
		list = append(list, c)
	}
	f.chunks[documentID] = list
	return nil
}

// DeleteChunksByFile 真实删除某文件的全部块（增量同步：文件变更/删除）。
func (f *FakeDocRepo) DeleteChunksByFile(_ context.Context, documentID int64, filePath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteChunksByFileLocked(documentID, filePath)
	return nil
}

// deleteChunksByFileLocked 调用方须已持有 f.mu。
func (f *FakeDocRepo) deleteChunksByFileLocked(documentID int64, filePath string) {
	var kept []model.Chunk
	for _, c := range f.chunks[documentID] {
		if c.FilePath != nil && *c.FilePath == filePath {
			continue
		}
		kept = append(kept, c)
	}
	f.chunks[documentID] = kept
}

// IsTaskRunning 与 repo 同口径（fencing 的快速失败检查）。
func (f *FakeDocRepo) IsTaskRunning(_ context.Context, taskID int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[taskID]
	return ok && t.Status == model.TaskStatusRunning, nil
}

// ReplaceFileChunksIfTaskRunning 与 repo 同口径（审查 #1 补强）：
// 任务不在 running 时返回 (false, nil) 且**不产生任何写入**——元数据与分块要么一起生效、要么都不生效。
func (f *FakeDocRepo) ReplaceFileChunksIfTaskRunning(_ context.Context, taskID, documentID int64, filePath string, file *model.DocumentFile, chunks []model.Chunk) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[taskID]; !ok || t.Status != model.TaskStatusRunning {
		return false, nil
	}
	if f.ErrOnReplace != nil {
		return false, f.ErrOnReplace
	}
	if file != nil {
		if err := f.upsertFileLocked(file); err != nil {
			return false, err
		}
		for i := range chunks {
			id := file.ID
			chunks[i].FileID = &id
		}
	}
	old := f.chunks[documentID]
	f.deleteChunksByFileLocked(documentID, filePath)
	if err := f.insertChunksLocked(documentID, chunks); err != nil {
		f.chunks[documentID] = old // 与真库事务语义一致：插入失败整笔回滚，旧块不得被删掉
		return false, err
	}
	return true, nil
}

// ReplaceDocumentChunksIfTaskRunning 与 repo 同口径（整篇替换带 fencing）。
func (f *FakeDocRepo) ReplaceDocumentChunksIfTaskRunning(_ context.Context, taskID, documentID int64, files []*model.DocumentFile, chunks []model.Chunk) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[taskID]; !ok || t.Status != model.TaskStatusRunning {
		return false, nil
	}
	if f.ErrOnReplace != nil {
		return false, f.ErrOnReplace
	}
	idByPath := make(map[string]int64, len(files))
	for _, file := range files {
		if err := f.upsertFileLocked(file); err != nil {
			return false, err
		}
		idByPath[file.Path] = file.ID
	}
	for i := range chunks {
		if chunks[i].FilePath == nil {
			continue
		}
		if id, ok := idByPath[*chunks[i].FilePath]; ok {
			chunks[i].FileID = &id
		}
	}
	stored := make([]model.Chunk, 0, len(chunks))
	for _, c := range chunks {
		if c.DocumentID == 0 {
			c.DocumentID = documentID
		}
		stored = append(stored, c)
	}
	f.chunks[documentID] = stored
	return true, nil
}

// MaintainFilesIfTaskRunning 与 repo 同口径（审查 #5）：任务非 running 时零写入。
func (f *FakeDocRepo) MaintainFilesIfTaskRunning(_ context.Context, taskID, documentID int64, removePaths []string, files []*model.DocumentFile) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[taskID]; !ok || t.Status != model.TaskStatusRunning {
		return false, nil
	}
	if len(removePaths) > 0 {
		del := map[string]bool{}
		for _, p := range removePaths {
			del[p] = true
		}
		var kept []model.DocumentFile
		for _, file := range f.files[documentID] {
			if !del[file.Path] {
				kept = append(kept, file)
			}
		}
		f.files[documentID] = kept
		for _, p := range removePaths {
			f.deleteChunksByFileLocked(documentID, p)
		}
	}
	for _, file := range files {
		if err := f.upsertFileLocked(file); err != nil {
			return false, err
		}
	}
	return true, nil
}

// UpdateDocumentIfTaskRunning 与 repo 同口径（审查 #5）：任务非 running 时不写文档，
// 否则僵尸 worker 会把 sweeper 已置 failed 的文档覆盖回 processing。
func (f *FakeDocRepo) UpdateDocumentIfTaskRunning(_ context.Context, taskID, documentID int64, updates map[string]any) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[taskID]; !ok || t.Status != model.TaskStatusRunning {
		return false, nil
	}
	d, ok := f.docs[documentID]
	if !ok {
		return false, nil
	}
	applyDocUpdates(d, updates)
	return true, nil
}

// FinishTaskIfRunning 与 repo 同口径（审查 #5）：任务终态与文档终态原子生效；
// 任务已被回收时两者都不写。
func (f *FakeDocRepo) FinishTaskIfRunning(_ context.Context, taskID, documentID int64, taskUpdates, docUpdates map[string]any) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[taskID]
	if !ok || t.Status != model.TaskStatusRunning {
		return false, nil
	}
	applyTaskUpdates(t, taskUpdates)
	if d, ok := f.docs[documentID]; ok && len(docUpdates) > 0 {
		applyDocUpdates(d, docUpdates)
	}
	return true, nil
}

// applyTaskUpdates 应用任务字段更新（与 repo 的 map Updates 同口径）。
func applyTaskUpdates(t *model.IngestTask, updates map[string]any) {
	if v, ok := updates["status"].(int16); ok {
		t.Status = v
	}
	if v, ok := updates["retries"].(int); ok {
		t.Retries = v
	}
	if v, ok := updates["error"]; ok {
		if v == nil {
			t.Error = nil
		} else if s, ok := v.(string); ok {
			msg := s
			t.Error = &msg
		}
	}
	if v, ok := updates["finished_at"].(time.Time); ok {
		ts := v
		t.FinishedAt = &ts
	}
}

// applyDocUpdates 应用文档字段更新（与 repo 的 map Updates 同口径，nil 表示清空）。
func applyDocUpdates(d *model.Document, updates map[string]any) {
	for k, v := range updates {
		switch k {
		case "status":
			if st, ok := v.(int16); ok {
				d.Status = st
			}
		case "stage":
			if v == nil {
				d.Stage = nil
			} else if st, ok := v.(int16); ok {
				stage := st
				d.Stage = &stage
			}
		case "error":
			if v == nil {
				d.Error = nil
			} else if s, ok := v.(string); ok {
				msg := s
				d.Error = &msg
			}
		case "file_count":
			switch n := v.(type) {
			case int:
				c := n
				d.FileCount = &c
			case int64:
				c := int(n)
				d.FileCount = &c
			}
		case "total_size":
			switch n := v.(type) {
			case int:
				s := int64(n)
				d.TotalSize = &s
			case int64:
				s := n
				d.TotalSize = &s
			}
		case "last_commit":
			if s, ok := v.(string); ok {
				c := s
				d.LastCommit = &c
			}
		case "last_synced_at":
			if ts, ok := v.(time.Time); ok {
				t := ts
				d.LastSyncedAt = &t
			}
		case "updated_at":
			if ts, ok := v.(time.Time); ok {
				d.UpdatedAt = ts
			}
		}
	}
}

// GetChunks 读取某文档的内存分块（断言用）。
func (f *FakeDocRepo) GetChunks(documentID int64) []model.Chunk {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.Chunk, 0, len(f.chunks[documentID]))
	out = append(out, f.chunks[documentID]...)
	return out
}

// ChunkCountByFile 某文件的分块数（断言用）。
func (f *FakeDocRepo) ChunkCountByFile(documentID int64, filePath string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.chunks[documentID] {
		if c.FilePath != nil && *c.FilePath == filePath {
			n++
		}
	}
	return n
}

// chunkKey 唯一键（document_id, file_path, chunk_index），与迁移 000003 一致。
func chunkKey(c model.Chunk) string {
	return fmt.Sprintf("%d|%s|%d", c.DocumentID, derefStr(c.FilePath), c.ChunkIndex)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (f *FakeDocRepo) CreateTask(_ context.Context, t *model.IngestTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnCreateTask != nil {
		return f.ErrOnCreateTask
	}
	// 与真库同口径（迁移 000006 部分唯一索引 uq_ingest_tasks_running）：
	// 同一文档最多一个排队/运行中任务，否则并发「重试/同步/重建」的缺陷在单测里测不出来（P1-10）。
	for _, ex := range f.tasks {
		if ex.DocumentID == t.DocumentID &&
			(ex.Status == model.TaskStatusQueued || ex.Status == model.TaskStatusRunning) {
			return repo.ErrTaskRunning
		}
	}
	t.ID = f.nextTaskID
	f.nextTaskID++
	t.CreatedAt = time.Now()
	cp := *t
	f.tasks[t.ID] = &cp
	return nil
}

func (f *FakeDocRepo) SaveTask(_ context.Context, t *model.IngestTask) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnSaveTask != nil {
		return f.ErrOnSaveTask
	}
	old, ok := f.tasks[t.ID]
	if !ok {
		return repo.ErrNotFound
	}
	cp := *t
	// 与 repo 同口径（Omit heartbeat_at）：SaveTask 不用内存里的旧值覆盖心跳列，
	// 该列只由 HeartbeatTask 维护。
	cp.HeartbeatAt = old.HeartbeatAt
	f.tasks[t.ID] = &cp
	return nil
}

func (f *FakeDocRepo) GetTaskByID(_ context.Context, id int64) (*model.IngestTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *t
	return &cp, nil
}

// TransitionTaskIfRunning 与 repo 同口径（审查 #1）：仅当任务仍为 running 时应用 updates；
// 任务不存在或已终结返回 (false, nil)，不视为错误。
func (f *FakeDocRepo) TransitionTaskIfRunning(_ context.Context, taskID int64, updates map[string]any) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[taskID]
	if !ok || t.Status != model.TaskStatusRunning {
		return false, nil
	}
	applyTaskUpdates(t, updates)
	return true, nil
}

func (f *FakeDocRepo) HasRunningTask(_ context.Context, documentID int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnHasRunning != nil {
		return false, f.ErrOnHasRunning
	}
	for _, t := range f.tasks {
		if t.DocumentID == documentID && (t.Status == model.TaskStatusQueued || t.Status == model.TaskStatusRunning) {
			return true, nil
		}
	}
	return false, nil
}

// ListAutoSyncDocs 返回 auto_sync 且非 processing 的 git 文档（M14）。
func (f *FakeDocRepo) ListAutoSyncDocs(_ context.Context) ([]model.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var docs []model.Document
	for _, d := range f.docs {
		if d.Type == model.DocTypeGit && d.AutoSync && d.Status != model.DocStatusProcessing {
			docs = append(docs, *d)
		}
	}
	return docs, nil
}

func (f *FakeDocRepo) ListTaskHistory(_ context.Context, documentID int64, limit int) ([]repo.TaskRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ErrOnHistory != nil {
		return nil, f.ErrOnHistory
	}
	var rows []repo.TaskRow
	for _, t := range f.tasks {
		if t.DocumentID == documentID {
			rows = append(rows, repo.TaskRow{
				ID: t.ID, Type: t.Type, Status: t.Status, Retries: t.Retries, Error: t.Error,
				CreatedAt: t.CreatedAt, StartedAt: t.StartedAt, FinishedAt: t.FinishedAt,
			})
		}
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// FailZombieTasks 与 repo 实现同口径（P1-8 + 验收回归 8-1）：
//   - worker_id = workerID 的运行中任务：本实例上次运行留下的残留，立即回收；
//   - 心跳（缺失时回落 started_at / created_at）超过 timeoutMinutes 的运行中任务：跨实例僵尸；
//   - 排队中（status=1）的任务**无论归属如何**都不动——交给 asynq 重投，否则会造成库/队列不一致。
//
// 返回任务数与被波及文档 ID。
func (f *FakeDocRepo) FailZombieTasks(_ context.Context, workerID string, timeoutMinutes int) (int64, []int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if timeoutMinutes <= 0 {
		timeoutMinutes = 5
	}
	cutoff := time.Now().Add(-time.Duration(timeoutMinutes) * time.Minute)
	var (
		n      int64
		docIDs []int64
	)
	seen := map[int64]bool{}
	now := time.Now()
	msg := zombieErr
	for id, t := range f.tasks {
		if t.Status != model.TaskStatusRunning {
			continue
		}
		own := t.WorkerID != nil && *t.WorkerID == workerID
		stale := taskLeaseTime(t).Before(cutoff)
		if !own && !stale {
			continue // 其它实例有心跳的活任务：不得清理（P1-8 的核心断言）
		}
		t.Status = model.TaskStatusFailed
		t.Error = &msg
		t.FinishedAt = &now
		f.tasks[id] = t
		n++
		if !seen[t.DocumentID] {
			seen[t.DocumentID] = true
			docIDs = append(docIDs, t.DocumentID)
		}
	}
	return n, docIDs, nil
}

// FailStaleTasks 周期性僵尸回收，与 repo 实现同口径：**只看心跳时间，不带归属条件**
// （带归属会杀掉本实例正在处理的任务）。
func (f *FakeDocRepo) FailStaleTasks(_ context.Context, timeoutMinutes int) (int64, []int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if timeoutMinutes <= 0 {
		timeoutMinutes = 5
	}
	cutoff := time.Now().Add(-time.Duration(timeoutMinutes) * time.Minute)
	var (
		n      int64
		docIDs []int64
	)
	seen := map[int64]bool{}
	now := time.Now()
	msg := zombieErr
	for id, t := range f.tasks {
		if t.Status != model.TaskStatusRunning || !taskLeaseTime(t).Before(cutoff) {
			continue
		}
		t.Status = model.TaskStatusFailed
		t.Error = &msg
		t.FinishedAt = &now
		f.tasks[id] = t
		n++
		if !seen[t.DocumentID] {
			seen[t.DocumentID] = true
			docIDs = append(docIDs, t.DocumentID)
		}
	}
	return n, docIDs, nil
}

// HeartbeatTask 刷新运行中任务的心跳（真内存写时间），与 repo 实现同口径：
// 只对 status=2 生效；任务行不存在（已随文档级联删除）时返回 nil，与真库「更新 0 行」一致。
func (f *FakeDocRepo) HeartbeatTask(_ context.Context, taskID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[taskID]
	if !ok || t.Status != model.TaskStatusRunning {
		return nil
	}
	now := time.Now()
	t.HeartbeatAt = &now
	return nil
}

// taskLeaseTime 任务「最后活动时刻」：心跳优先，缺失时回落 started_at，再回落 created_at。
// 与 repo 的 COALESCE(heartbeat_at, started_at, created_at) 同口径。
func taskLeaseTime(t *model.IngestTask) time.Time {
	if t.HeartbeatAt != nil {
		return *t.HeartbeatAt
	}
	if t.StartedAt != nil {
		return *t.StartedAt
	}
	return t.CreatedAt
}

// BackdateTask 把任务的心跳/开始/创建时间整体回拨 ago（构造「确实已死的僵尸」用）。
func (f *FakeDocRepo) BackdateTask(taskID int64, ago time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[taskID]
	if !ok {
		return
	}
	at := time.Now().Add(-ago)
	t.CreatedAt = at
	if t.StartedAt != nil {
		t.StartedAt = &at
	}
	if t.HeartbeatAt != nil {
		t.HeartbeatAt = &at
	}
}

// FailProcessingDocs 只把给定文档中仍是处理中的置失败（不做全库 UPDATE）。
func (f *FakeDocRepo) FailProcessingDocs(_ context.Context, docIDs []int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	now := time.Now()
	msg := zombieErr
	for _, id := range docIDs {
		d, ok := f.docs[id]
		if !ok || d.Status != model.DocStatusProcessing {
			continue
		}
		d.Status = model.DocStatusFailed
		d.Stage = nil
		d.Error = &msg
		d.UpdatedAt = now
		f.docs[id] = d
		n++
	}
	return n, nil
}

// zombieErr 重启恢复写台账的错误摘要（与 repo 侧一致）。
const zombieErr = "服务重启导致任务中断"

// GetString 设置读取（worker pipeline 测试用；与 FakeCache 无关）。
func (f *FakeDocRepo) GetString(_ context.Context, _, def string) (string, error) { return def, nil }

// ---- 测试辅助 ----

// SeedDoc 直接写入一个文档（绕过 service）。
func (f *FakeDocRepo) SeedDoc(kbID int64, typ int16, status int16) *model.Document {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := &model.Document{ID: f.nextDoc, KBID: kbID, Type: typ, Status: status, CreatedBy: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.nextDoc++
	f.docs[d.ID] = d
	return d
}

// SeedTask 直接写入一个排队任务。
func (f *FakeDocRepo) SeedTask(docID int64, typ int16) *model.IngestTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &model.IngestTask{ID: f.nextTaskID, DocumentID: docID, Type: typ, Status: model.TaskStatusQueued, CreatedAt: time.Now()}
	f.nextTaskID++
	f.tasks[t.ID] = t
	return t
}

// SeedTaskWithWorker 写入指定归属与状态的任务（P1-8 多实例场景构造用；
// 绕过 CreateTask 的唯一约束检查，便于造出「两个 worker 各跑一个」的合法前置状态）。
// workerID 传空串表示迁移前的历史数据（worker_id IS NULL）。
// 运行中任务同步写 started_at（真库 Process 进入 running 时必然写，供心跳缺失时的时间兜底判定）。
func (f *FakeDocRepo) SeedTaskWithWorker(docID int64, typ int16, status int16, workerID string) *model.IngestTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	t := &model.IngestTask{
		ID: f.nextTaskID, DocumentID: docID, Type: typ, Status: status,
		CreatedAt: now,
	}
	if status == model.TaskStatusRunning {
		t.StartedAt = &now
	}
	if workerID != "" {
		t.WorkerID = &workerID
	}
	f.nextTaskID++
	f.tasks[t.ID] = t
	return t
}

// DocCount 文档总数（P1-9 回滚断言用）。
func (f *FakeDocRepo) DocCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.docs)
}

// TaskCount 任务总数（P1-9 回滚断言用）。
func (f *FakeDocRepo) TaskCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tasks)
}

// RunningTaskCount 指定文档的排队/运行中任务数（P1-10 唯一性断言用）。
func (f *FakeDocRepo) RunningTaskCount(docID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, t := range f.tasks {
		if t.DocumentID == docID && (t.Status == model.TaskStatusQueued || t.Status == model.TaskStatusRunning) {
			n++
		}
	}
	return n
}

// SetFileStatus 覆盖某文档某路径的文件记录（增量 diff 断言用）。
func (f *FakeDocRepo) SetFile(docID int64, path string, hash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[docID] = append(f.files[docID], model.DocumentFile{
		ID: f.nextFile, DocumentID: docID, Path: path, Status: model.FileStatusIndexed, ContentHash: &hash,
	})
	f.nextFile++
}

// FileCount 该文档清单条数（断言用）。
func (f *FakeDocRepo) FileCount(docID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.files[docID])
}

// TaskByID 断言用读取任务。
func (f *FakeDocRepo) TaskByID(id int64) *model.IngestTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := f.tasks[id]
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}

// FinishTasks 把文档的任务全部置为成功（模拟 worker 消费完毕）。
func (f *FakeDocRepo) FinishTasks(docID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, t := range f.tasks {
		if t.DocumentID == docID && (t.Status == model.TaskStatusQueued || t.Status == model.TaskStatusRunning) {
			t.Status = model.TaskStatusSuccess
			f.tasks[id] = t
		}
	}
}

// DocByID 断言用读取文档。
func (f *FakeDocRepo) DocByID(id int64) *model.Document {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.docs[id]
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}
