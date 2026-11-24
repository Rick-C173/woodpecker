package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"woodpecker/internal/model"
	"woodpecker/internal/rag"
	"woodpecker/internal/repo"
)

// 错误码（与 httpx errcode 对应，API设计.md §2）。
const (
	CodeDocRepoTooLarge = "DOC_001"          // 422 仓库超过大小/文件数限制
	CodeDocUnsupported  = "DOC_002"          // 422 文件格式不支持
	CodeDocUnreachable  = "DOC_003"          // 422 仓库凭证无效或地址不可达
	CodeDocSyncFailed   = "DOC_004"          // 422 同步失败（远端不可达），旧内容保持可用
	CodeDocTaskRunning  = "DOC_TASK_RUNNING" // 409 已有运行中任务
)

// R2 限制与配额（PRD §6）。
const (
	maxUploadFiles     = 50
	maxUploadFileBytes = 5 << 20
	maxRepoBytes       = 1 << 30
	maxRepoFiles       = 50000
	maxKBDocuments     = 1000
	maxKBNameDocLen    = 500
)

// SettingGetter 设置读取（repo.SettingRepo 满足）。
type SettingGetter interface {
	GetString(ctx context.Context, key, def string) (string, error)
}

// Enqueuer 摄取任务入队（worker 包 asynq 实现；测试用假实现）。
type Enqueuer interface {
	EnqueueIngest(ctx context.Context, taskID int64) error
}

// DocRepo 文档存储接口（internal/repo.DocumentRepo 满足）。
type DocRepo interface {
	CreateDocument(ctx context.Context, d *model.Document) error
	SaveDocument(ctx context.Context, d *model.Document) error
	GetDocumentByID(ctx context.Context, id int64) (*model.Document, error)
	CountByKB(ctx context.Context, kbID int64) (int64, error)
	ListDocuments(ctx context.Context, q repo.DocumentListQuery) ([]repo.DocumentRow, int64, error)
	DeleteDocument(ctx context.Context, id int64) error
	UpsertFile(ctx context.Context, f *model.DocumentFile) error
	ListFiles(ctx context.Context, documentID int64) ([]model.DocumentFile, error)
	DeleteFilesByPath(ctx context.Context, documentID int64, paths []string) error
	CreateTask(ctx context.Context, t *model.IngestTask) error
	SaveTask(ctx context.Context, t *model.IngestTask) error
	GetTaskByID(ctx context.Context, id int64) (*model.IngestTask, error)
	HasRunningTask(ctx context.Context, documentID int64) (bool, error)
	ListTaskHistory(ctx context.Context, documentID int64, limit int) ([]repo.TaskRow, error)
}

// UploadFile 上传的单个文件（handler 解析 multipart 后传入）。
type UploadFile struct {
	Filename string
	Content  []byte
}

// DocumentItem 文档列表/详情条目。
type DocumentItem struct {
	ID           int64   `json:"id"`
	Type         string  `json:"type"`
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	Stage        *string `json:"stage,omitempty"`
	Error        string  `json:"error,omitempty"`
	RepoURL      string  `json:"repo_url,omitempty"`
	GitRef       string  `json:"git_ref,omitempty"`
	SourceURL    string  `json:"source_url,omitempty"`
	FileCount    int     `json:"file_count"`
	TotalSize    int64   `json:"total_size"`
	LastSyncedAt string  `json:"last_synced_at,omitempty"`
	CanSync      bool    `json:"can_sync"`    // git 类型且编辑者以上
	CanReindex   bool    `json:"can_reindex"` // 管理者以上
	CanRetry     bool    `json:"can_retry"`   // 失败态
	CreatedBy    int64   `json:"created_by"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// DocumentDetail 文档详情。
type DocumentDetail struct {
	DocumentItem
	Files []DocumentFileItem `json:"files"`
	Tasks []DocumentTaskItem `json:"tasks"`
}

// DocumentFileItem 文件清单条目。
type DocumentFileItem struct {
	ID       int64  `json:"id"`
	Path     string `json:"path"`
	Status   string `json:"status"` // indexed | excluded | failed
	Language string `json:"language,omitempty"`
	Size     int64  `json:"size"`
	Error    string `json:"error,omitempty"`
}

// DocumentTaskItem 任务历史条目。
type DocumentTaskItem struct {
	ID         int64  `json:"id"`
	Type       string `json:"type"` // full | sync
	Status     string `json:"status"`
	Retries    int    `json:"retries"`
	Error      string `json:"error,omitempty"`
	CreatedAt  string `json:"created_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// DocumentService 文档接入与摄取编排（FR-03/04/05/06/11、R1/R1b/R2/R3）。
type DocumentService struct {
	docs      DocRepo
	settings  SettingGetter
	cipher    ModelCipher
	queue     Enqueuer
	audit     *AuditService
	now       func() time.Time
	uploadDir string
}

func NewDocumentService(docs DocRepo, settings SettingGetter, cipher ModelCipher, queue Enqueuer, audit *AuditService) *DocumentService {
	return &DocumentService{docs: docs, settings: settings, cipher: cipher, queue: queue, audit: audit, now: time.Now, uploadDir: "data/uploads"}
}

// SetUploadDir 修改上传目录（配置注入/测试用）。
func (s *DocumentService) SetUploadDir(dir string) { s.uploadDir = dir }

// SetClock 注入时钟（测试用）。
func (s *DocumentService) SetClock(now func() time.Time) { s.now = now }

// ExclusionRules 读取 R1b 排除清单（settings 可覆盖）。
func (s *DocumentService) ExclusionRules(ctx context.Context) rag.ExclusionRules {
	raw, err := s.settings.GetString(ctx, "exclusion_rules", "")
	if err != nil {
		return rag.ParseExclusionRules("")
	}
	return rag.ParseExclusionRules(raw)
}

// checkKBCapacity R2：每库文档数 ≤ 1000。
func (s *DocumentService) checkKBCapacity(ctx context.Context, kbID int64) error {
	n, err := s.docs.CountByKB(ctx, kbID)
	if err != nil {
		return err
	}
	if n >= maxKBDocuments {
		return NewServiceError(CodeDocRepoTooLarge, fmt.Sprintf("知识库文档数已达上限 %d", maxKBDocuments))
	}
	return nil
}

// newDocAndTask 创建文档 + 排队任务并入队；返回文档 ID。
func (s *DocumentService) newDocAndTask(ctx context.Context, d *model.Document, taskType int16, ip string) (int64, error) {
	now := s.now()
	d.CreatedAt, d.UpdatedAt = now, now
	d.Status = model.DocStatusPending
	if err := s.docs.CreateDocument(ctx, d); err != nil {
		return 0, err
	}
	t := &model.IngestTask{DocumentID: d.ID, Type: taskType, Status: model.TaskStatusQueued, CreatedAt: now}
	if err := s.docs.CreateTask(ctx, t); err != nil {
		return 0, err
	}
	if err := s.queue.EnqueueIngest(ctx, t.ID); err != nil {
		return 0, fmt.Errorf("任务入队: %w", err)
	}
	s.audit.Record(ctx, &d.CreatedBy, "document.create", "document", &d.ID,
		map[string]any{"type": d.Type, "name": d.Name}, ip)
	return d.ID, nil
}

// CreateGit 接入 Git 仓库（编辑者+，FR-03）。凭证加密落库，不回显。
func (s *DocumentService) CreateGit(ctx context.Context, actorID, kbID int64, repoURL, gitRef, credential string, ignoreRules []string, ip string) (*DocumentItem, error) {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" || len(repoURL) > 1000 {
		return nil, NewServiceError(CodeValidation, "repo_url 不能为空且不超过 1000 字符")
	}
	if !strings.HasPrefix(repoURL, "http://") && !strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "git@") {
		return nil, NewServiceError(CodeValidation, "repo_url 必须是 http(s) 或 git@ 形式")
	}
	if gitRef != "" && len(gitRef) > 200 {
		return nil, NewServiceError(CodeValidation, "git_ref 不超过 200 字符")
	}
	if len(ignoreRules) > 50 {
		return nil, NewServiceError(CodeValidation, "ignore_rules 不超过 50 条")
	}
	if err := s.checkKBCapacity(ctx, kbID); err != nil {
		return nil, err
	}
	d := &model.Document{
		KBID: kbID, Type: model.DocTypeGit, Name: repoURL,
		RepoURL: &repoURL, IgnoreRules: ignoreRules, CreatedBy: actorID,
	}
	if gitRef != "" {
		d.GitRef = &gitRef
	}
	if cred := strings.TrimSpace(credential); cred != "" {
		enc, err := s.cipher.Encrypt([]byte(cred))
		if err != nil {
			return nil, err
		}
		d.CredentialEnc = enc
	}
	id, err := s.newDocAndTask(ctx, d, model.TaskTypeFull, ip)
	if err != nil {
		return nil, err
	}
	return s.itemOf(d, id), nil
}

// CreateUpload 上传文件（编辑者+，FR-04）：每文件一个文档；不支持格式逐个报 DOC_002。
func (s *DocumentService) CreateUpload(ctx context.Context, actorID, kbID int64, files []UploadFile, ip string) ([]*DocumentItem, []ServiceError, error) {
	if len(files) == 0 {
		return nil, nil, NewServiceError(CodeValidation, "至少上传一个文件")
	}
	if len(files) > maxUploadFiles {
		return nil, nil, NewServiceError(CodeDocRepoTooLarge, fmt.Sprintf("单次上传不超过 %d 个文件", maxUploadFiles))
	}
	if err := s.checkKBCapacity(ctx, kbID); err != nil {
		return nil, nil, err
	}
	var items []*DocumentItem
	var rejected []ServiceError
	for _, f := range files {
		name := filepath.ToSlash(filepath.Base(f.Filename))
		if name == "" || name == "." || strings.Contains(name, "..") {
			rejected = append(rejected, *NewServiceError(CodeValidation, "文件名非法"))
			continue
		}
		if !rag.IsSupportedExt(name) {
			rejected = append(rejected, *NewServiceError(CodeDocUnsupported, fmt.Sprintf("%s：格式不支持", name)))
			continue
		}
		if len(f.Content) > maxUploadFileBytes {
			rejected = append(rejected, *NewServiceError(CodeDocRepoTooLarge, fmt.Sprintf("%s：超过单文件 5MB 上限", name)))
			continue
		}
		d := &model.Document{KBID: kbID, Type: model.DocTypeUpload, Name: name, CreatedBy: actorID}
		id, err := s.newDocAndTask(ctx, d, model.TaskTypeFull, ip)
		if err != nil {
			return nil, rejected, err
		}
		// 内容落地：{uploadDir}/{kbID}/{docID}/{filename}
		dir := filepath.Join(s.uploadDir, fmt.Sprint(kbID), fmt.Sprint(id))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, rejected, fmt.Errorf("创建上传目录: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), f.Content, 0o600); err != nil {
			return nil, rejected, fmt.Errorf("写入上传文件: %w", err)
		}
		items = append(items, s.itemOf(d, id))
	}
	return items, rejected, nil
}

// CreateURL 接入网页（编辑者+，FR-05）。
func (s *DocumentService) CreateURL(ctx context.Context, actorID, kbID int64, rawURL, ip string) (*DocumentItem, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || len(rawURL) > 2000 {
		return nil, NewServiceError(CodeValidation, "url 不能为空且不超过 2000 字符")
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, NewServiceError(CodeValidation, "url 必须以 http:// 或 https:// 开头")
	}
	if err := s.checkKBCapacity(ctx, kbID); err != nil {
		return nil, err
	}
	d := &model.Document{KBID: kbID, Type: model.DocTypeURL, Name: rawURL, SourceURL: &rawURL, CreatedBy: actorID}
	id, err := s.newDocAndTask(ctx, d, model.TaskTypeFull, ip)
	if err != nil {
		return nil, err
	}
	return s.itemOf(d, id), nil
}

// List 文档列表（成员，API设计.md §3.6）。
func (s *DocumentService) List(ctx context.Context, kbID int64, typ, status int16, page, perPage int) ([]DocumentItem, int64, error) {
	rows, total, err := s.docs.ListDocuments(ctx, repo.DocumentListQuery{KBID: kbID, Type: typ, Status: status, Page: page, PerPage: perPage})
	if err != nil {
		return nil, 0, err
	}
	items := make([]DocumentItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.rowToItem(r))
	}
	return items, total, nil
}

// Get 文档详情（成员）：状态、错误、文件清单（含已排除）、任务历史。
func (s *DocumentService) Get(ctx context.Context, kbID, docID int64) (*DocumentDetail, error) {
	d, err := s.getOwned(ctx, kbID, docID)
	if err != nil {
		return nil, err
	}
	detail := &DocumentDetail{DocumentItem: *s.itemOf(d, d.ID)}
	files, err := s.docs.ListFiles(ctx, docID)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		item := DocumentFileItem{ID: f.ID, Path: f.Path, Status: fileStatusName(f.Status)}
		if f.Language != nil {
			item.Language = *f.Language
		}
		if f.Size != nil {
			item.Size = *f.Size
		}
		if f.Error != nil {
			item.Error = *f.Error
		}
		detail.Files = append(detail.Files, item)
	}
	tasks, err := s.docs.ListTaskHistory(ctx, docID, 20)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		item := DocumentTaskItem{ID: t.ID, Type: taskTypeName(t.Type), Status: taskStatusName(t.Status), Retries: t.Retries, CreatedAt: t.CreatedAt.Format("2006-01-02 15:04:05")}
		if t.Error != nil {
			item.Error = *t.Error
		}
		if t.FinishedAt != nil {
			item.FinishedAt = t.FinishedAt.Format("2006-01-02 15:04:05")
		}
		detail.Tasks = append(detail.Tasks, item)
	}
	return detail, nil
}

// Delete 删除文档（管理者+，R4：分块/文件清单/任务级联）。
func (s *DocumentService) Delete(ctx context.Context, actorID, kbID, docID int64, ip string) error {
	if _, err := s.getOwned(ctx, kbID, docID); err != nil {
		return err
	}
	if err := s.docs.DeleteDocument(ctx, docID); err != nil {
		return err
	}
	s.audit.Record(ctx, &actorID, "document.delete", "document", &docID, map[string]any{"kb_id": kbID}, ip)
	return nil
}

// Retry 重试失败文档（编辑者+，R3：产生全新任务，不续用旧任务）。
func (s *DocumentService) Retry(ctx context.Context, actorID, kbID, docID int64, ip string) error {
	d, err := s.getOwned(ctx, kbID, docID)
	if err != nil {
		return err
	}
	if d.Status != model.DocStatusFailed {
		return NewServiceError(CodeValidation, "仅失败状态的文档可重试")
	}
	return s.enqueueNewTask(ctx, actorID, d, model.TaskTypeFull, ip)
}

// Reindex 重建索引（管理者+）：仅已就绪态。
func (s *DocumentService) Reindex(ctx context.Context, actorID, kbID, docID int64, ip string) error {
	d, err := s.getOwned(ctx, kbID, docID)
	if err != nil {
		return err
	}
	if d.Status != model.DocStatusReady {
		return NewServiceError(CodeValidation, "仅已就绪的文档可重建索引")
	}
	return s.enqueueNewTask(ctx, actorID, d, model.TaskTypeFull, ip)
}

// Sync 手动同步（编辑者+，FR-03）：仅 git 类型；增量摄取，比对 last_commit。
func (s *DocumentService) Sync(ctx context.Context, actorID, kbID, docID int64, ip string) error {
	d, err := s.getOwned(ctx, kbID, docID)
	if err != nil {
		return err
	}
	if d.Type != model.DocTypeGit {
		return NewServiceError(CodeValidation, "仅 Git 类型文档支持同步")
	}
	if d.Status == model.DocStatusProcessing {
		return NewServiceError(CodeValidation, "文档处理中，无需同步")
	}
	return s.enqueueNewTask(ctx, actorID, d, model.TaskTypeSync, ip)
}

// enqueueNewTask 幂等入队：存在运行中任务 → 409 DOC_TASK_RUNNING（API设计.md §7）。
func (s *DocumentService) enqueueNewTask(ctx context.Context, actorID int64, d *model.Document, taskType int16, ip string) error {
	running, err := s.docs.HasRunningTask(ctx, d.ID)
	if err != nil {
		return err
	}
	if running {
		return NewServiceError(CodeDocTaskRunning, "该文档已有运行中的摄取任务")
	}
	statusBefore := d.Status
	now := s.now()
	t := &model.IngestTask{DocumentID: d.ID, Type: taskType, Status: model.TaskStatusQueued, CreatedAt: now}
	if err := s.docs.CreateTask(ctx, t); err != nil {
		return err
	}
	// 文档回到待处理态并清除上次错误
	d.Status = model.DocStatusPending
	d.Error = nil
	d.UpdatedAt = now
	if err := s.docs.SaveDocument(ctx, d); err != nil {
		return err
	}
	if err := s.queue.EnqueueIngest(ctx, t.ID); err != nil {
		return fmt.Errorf("任务入队: %w", err)
	}
	action := "document.retry"
	switch taskType {
	case model.TaskTypeSync:
		action = "document.sync"
	case model.TaskTypeFull:
		if statusBefore == model.DocStatusReady {
			action = "document.reindex"
		}
	}
	s.audit.Record(ctx, &actorID, action, "document", &d.ID,
		map[string]any{"task_id": t.ID, "task_type": taskType}, ip)
	return nil
}

// getOwned 校验文档属于该知识库（非本库文档 404 不泄露）。
func (s *DocumentService) getOwned(ctx context.Context, kbID, docID int64) (*model.Document, error) {
	d, err := s.docs.GetDocumentByID(ctx, docID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, NewServiceError(CodeNotFound, "文档不存在")
		}
		return nil, err
	}
	if d.KBID != kbID {
		return nil, NewServiceError(CodeNotFound, "文档不存在")
	}
	return d, nil
}

// itemOf 从已落库的文档构造列表条目（id 为库中主键）。
func (s *DocumentService) itemOf(d *model.Document, id int64) *DocumentItem {
	var fc int
	var ts int64
	if d.FileCount != nil {
		fc = *d.FileCount
	}
	if d.TotalSize != nil {
		ts = *d.TotalSize
	}
	item := s.rowToItem(repo.DocumentRow{
		ID: id, Type: d.Type, Name: d.Name, Status: d.Status, Stage: d.Stage,
		Error: d.Error, SourceURL: d.SourceURL, RepoURL: d.RepoURL, GitRef: d.GitRef,
		FileCount: &fc, TotalSize: &ts, LastSyncedAt: d.LastSyncedAt,
		CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	})
	// FileCount/TotalSize 指针指向临时变量：立即拷贝值，避免悬挂语义
	item.FileCount = fc
	item.TotalSize = ts
	return &item
}

func (s *DocumentService) rowToItem(r repo.DocumentRow) DocumentItem {
	item := DocumentItem{
		ID: r.ID, Type: docTypeName(r.Type), Name: r.Name,
		Status: docStatusName(r.Status), Stage: stageName(r.Stage),
		FileCount: int64PtrCount(r.FileCount), TotalSize: int64PtrVal(r.TotalSize),
		LastSyncedAt: timePtrString(r.LastSyncedAt),
		CreatedBy:    r.CreatedBy,
		CreatedAt:    r.CreatedAt.Format("2006-01-02 15:04:05"),
		UpdatedAt:    r.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
	if r.Error != nil {
		item.Error = *r.Error
	}
	if r.RepoURL != nil {
		item.RepoURL = *r.RepoURL
	}
	if r.GitRef != nil {
		item.GitRef = *r.GitRef
	}
	if r.SourceURL != nil {
		item.SourceURL = *r.SourceURL
	}
	item.CanSync = r.Type == model.DocTypeGit && (r.Status == model.DocStatusReady || r.Status == model.DocStatusFailed)
	item.CanReindex = r.Status == model.DocStatusReady
	item.CanRetry = r.Status == model.DocStatusFailed
	return item
}

func docTypeName(t int16) string {
	switch t {
	case model.DocTypeGit:
		return "git"
	case model.DocTypeUpload:
		return "upload"
	case model.DocTypeURL:
		return "url"
	}
	return ""
}

func docStatusName(s int16) string {
	switch s {
	case model.DocStatusPending:
		return "pending"
	case model.DocStatusProcessing:
		return "processing"
	case model.DocStatusReady:
		return "ready"
	case model.DocStatusFailed:
		return "failed"
	}
	return ""
}

func stageName(stage *int16) *string {
	if stage == nil {
		return nil
	}
	var v string
	switch *stage {
	case model.DocStageParse:
		v = "parsing"
	case model.DocStageChunk:
		v = "chunking"
	case model.DocStageEmbed:
		v = "embedding"
	default:
		return nil
	}
	return &v
}

func fileStatusName(s int16) string {
	switch s {
	case model.FileStatusIndexed:
		return "indexed"
	case model.FileStatusExcluded:
		return "excluded"
	case model.FileStatusFailed:
		return "failed"
	}
	return ""
}

func taskTypeName(t int16) string {
	if t == model.TaskTypeSync {
		return "sync"
	}
	return "full"
}

func taskStatusName(s int16) string {
	switch s {
	case model.TaskStatusQueued:
		return "queued"
	case model.TaskStatusRunning:
		return "running"
	case model.TaskStatusSuccess:
		return "success"
	case model.TaskStatusFailed:
		return "failed"
	}
	return ""
}

func int64PtrCount(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func int64PtrVal(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func timePtrString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
