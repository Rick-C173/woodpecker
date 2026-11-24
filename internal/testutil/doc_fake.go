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
	f.DeletedDoc = append(f.DeletedDoc, id)
	return nil
}

func (f *FakeDocRepo) UpsertFile(_ context.Context, file *model.DocumentFile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	var kept []model.Chunk
	for _, c := range f.chunks[documentID] {
		if c.FilePath != nil && *c.FilePath == filePath {
			continue
		}
		kept = append(kept, c)
	}
	f.chunks[documentID] = kept
	return nil
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
	if _, ok := f.tasks[t.ID]; !ok {
		return repo.ErrNotFound
	}
	cp := *t
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

func (f *FakeDocRepo) FailZombieTasks(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for id, t := range f.tasks {
		if t.Status == model.TaskStatusQueued || t.Status == model.TaskStatusRunning {
			t.Status = model.TaskStatusFailed
			f.tasks[id] = t
			n++
		}
	}
	return n, nil
}

func (f *FakeDocRepo) FailProcessingDocs(_ context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for id, d := range f.docs {
		if d.Status == model.DocStatusProcessing {
			d.Status = model.DocStatusFailed
			f.docs[id] = d
			n++
		}
	}
	return n, nil
}

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
