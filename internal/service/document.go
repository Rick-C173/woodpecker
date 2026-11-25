package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// 凭证类型（API设计.md §5 / 数据库设计.md §3.2：credential = {"kind","secret"}）。
const (
	CredentialKindToken = "token" // HTTPS 访问令牌（放 BasicAuth 密码位）
	CredentialKindSSH   = "ssh"   // SSH 私钥（PEM 明文），走 PublicKeys 认证
)

// GitCredential Git 仓库凭证明文结构（落库前 JSON 序列化后 AES-GCM 加密）。
// 零值表示匿名访问；旧数据（加密的是裸 token 字符串）由 rag.ParseCredential 兼容。
type GitCredential struct {
	Kind   string `json:"kind"`   // token | ssh
	Secret string `json:"secret"` // token 明文或 SSH 私钥 PEM
}

// validateGitCredential 凭证校验（422）：kind 必须为 token/ssh，secret 非空。
// 兼容旧客户端：kind 缺省但 secret 非空时按 token 处理（审计 P1-12 向后兼容）。
func validateGitCredential(c GitCredential) (GitCredential, error) {
	c.Kind = strings.ToLower(strings.TrimSpace(c.Kind))
	c.Secret = strings.TrimSpace(c.Secret)
	if c.Secret == "" {
		return GitCredential{}, nil // 匿名访问
	}
	if c.Kind == "" {
		c.Kind = CredentialKindToken
	}
	switch c.Kind {
	case CredentialKindToken:
		if len(c.Secret) > maxCredentialLen {
			return GitCredential{}, NewServiceError(CodeValidation, "凭证长度超过上限")
		}
	case CredentialKindSSH:
		if len(c.Secret) > maxSSHKeyLen {
			return GitCredential{}, NewServiceError(CodeValidation, "SSH 私钥长度超过上限")
		}
		if !strings.Contains(c.Secret, "PRIVATE KEY") {
			return GitCredential{}, NewServiceError(CodeValidation, "credential.kind=ssh 时 secret 必须是 PEM 私钥")
		}
	default:
		return GitCredential{}, NewServiceError(CodeValidation, "credential.kind 必须是 token 或 ssh")
	}
	return c, nil
}

// R2 限制与配额（PRD §6）。
const (
	maxUploadFiles     = 50
	maxUploadFileBytes = 5 << 20
	maxRepoBytes       = 1 << 30
	maxRepoFiles       = 50000
	maxKBDocuments     = 1000
	maxKBNameDocLen    = 500
	maxCredentialLen   = 4096  // token 长度上限
	maxSSHKeyLen       = 16384 // SSH 私钥（PEM）长度上限
)

// 导出给 handler 做上传前置限制复用（避免两处魔数漂移，审计 P2-28）。
const (
	// MaxUploadFiles 单次上传文件数上限（R2）。
	MaxUploadFiles = maxUploadFiles
	// MaxUploadFileBytes 单文件字节上限（R2：5MB）。
	MaxUploadFileBytes = maxUploadFileBytes
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
	CreateDocumentWithTask(ctx context.Context, d *model.Document, t *model.IngestTask) error
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
	// 文档 ID
	ID int64 `json:"id"`
	// 来源类型
	Type string `json:"type"`
	// 名称（仓库名/文件名/网页标题）
	Name string `json:"name"`
	// 摄取状态
	Status string `json:"status"`
	// processing 阶段：parsing/chunking/embedding（其他状态为 null）
	Stage *string `json:"stage,omitempty"`
	// 失败原因（仅 failed）
	Error string `json:"error,omitempty"`
	// 仓库地址（仅 git）
	RepoURL string `json:"repo_url,omitempty"`
	// 分支/标签（仅 git，空=默认分支）
	GitRef string `json:"git_ref,omitempty"`
	// 网页地址（仅 url）
	SourceURL string `json:"source_url,omitempty"`
	// 已入库文件数（含已排除）
	FileCount int `json:"file_count"`
	// 入库文件总字节数
	TotalSize int64 `json:"total_size"`
	// 最近一次同步完成时间 RFC3339（未同步过为空）
	LastSyncedAt string `json:"last_synced_at,omitempty"`
	// 当前用户可否手动同步（git 类型且编辑者+）
	CanSync bool `json:"can_sync"` // git 类型且编辑者以上
	// 当前用户可否重建索引（管理者+且 ready）
	CanReindex bool `json:"can_reindex"` // 管理者以上
	// 当前用户可否重试（失败态且编辑者+）
	CanRetry bool `json:"can_retry"` // 失败态
	// 创建人用户 ID
	CreatedBy int64 `json:"created_by"`
	// 创建时间 RFC3339 UTC
	CreatedAt string `json:"created_at"`
	// 最近更新时间 RFC3339 UTC
	UpdatedAt string `json:"updated_at"`
}

// DocumentDetail 文档详情。
type DocumentDetail struct {
	DocumentItem
	// 文件清单（git 仓库=仓库内文件，upload=上传文件，url=抓取页面）
	Files []DocumentFileItem `json:"files"`
	// 任务历史（新到旧）
	Tasks []DocumentTaskItem `json:"tasks"`
}

// DocumentFileItem 文件清单条目。
type DocumentFileItem struct {
	// 文件记录 ID
	ID int64 `json:"id"`
	// 仓库内相对路径（git/url）或文件名（upload）
	Path string `json:"path"`
	// 文件状态
	Status string `json:"status"` // indexed | excluded | failed
	// 语言（代码文件按扩展名推断）
	Language string `json:"language,omitempty"`
	// 字节数
	Size int64 `json:"size"`
	// 失败/排除原因（仅 failed/excluded 时可能携带）
	Error string `json:"error,omitempty"`
}

// DocumentTaskItem 任务历史条目。
type DocumentTaskItem struct {
	// 任务 ID
	ID int64 `json:"id"`
	// 任务类型
	Type string `json:"type"` // full | sync
	// 任务状态
	Status string `json:"status"`
	// 已重试次数
	Retries int `json:"retries"`
	// 失败原因（仅 failed）
	Error string `json:"error,omitempty"`
	// 任务创建时间 RFC3339
	CreatedAt string `json:"created_at"`
	// 完成时间 RFC3339（未完成为空）
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
//
// 顺序（审计 P1-9）：CreateDocument + CreateTask **同一事务** → beforeEnqueue（可选，上传场景
// 用它把已落盘的文件改名到 {docID} 目录）→ Enqueue。
// 任一步失败**整条回滚**：删除刚建的文档（ingest_tasks 由外键 ON DELETE CASCADE 一并删除），
// 不留下「status=待处理」的僵尸文档——Retry 只接受 failed 态，僵尸文档用户既看不到失败也重试不了。
// 回滚后用户可直接重新提交，故不采用「置 failed 允许 Retry」的第二方案（省一次人工重试）。
func (s *DocumentService) newDocAndTask(ctx context.Context, d *model.Document, taskType int16, ip string,
	beforeEnqueue func(docID int64) error) (int64, error) {
	now := s.now()
	d.CreatedAt, d.UpdatedAt = now, now
	d.Status = model.DocStatusPending
	t := &model.IngestTask{Type: taskType, Status: model.TaskStatusQueued, CreatedAt: now}
	if err := s.docs.CreateDocumentWithTask(ctx, d, t); err != nil {
		return 0, err
	}
	if beforeEnqueue != nil {
		if err := beforeEnqueue(d.ID); err != nil {
			s.rollbackNewDoc(ctx, d.ID)
			return 0, err
		}
	}
	if err := s.queue.EnqueueIngest(ctx, t.ID); err != nil {
		s.rollbackNewDoc(ctx, d.ID)
		return 0, fmt.Errorf("任务入队: %w", err)
	}
	s.audit.Record(ctx, &d.CreatedBy, "document.create", "document", &d.ID,
		map[string]any{"type": d.Type, "name": d.Name}, ip)
	return d.ID, nil
}

// rollbackNewDoc 补偿回滚：删除刚创建的文档（任务随外键级联删除）。
// 用 WithoutCancel：请求已断连/超时也必须完成补偿，否则照样留僵尸文档；
// 回滚失败只记日志，不覆盖调用方已拿到的原始错误。
func (s *DocumentService) rollbackNewDoc(ctx context.Context, docID int64) {
	if docID <= 0 {
		return
	}
	if err := s.docs.DeleteDocument(context.WithoutCancel(ctx), docID); err != nil {
		slog.Error("回滚新建文档失败", "doc_id", docID, "err", err)
	}
}

// CreateGit 接入 Git 仓库（编辑者+，FR-03）。凭证按 {kind,secret} JSON 序列化后 AES-GCM 加密落库，
// 不回显（审计 P1-12：kind 必须持久化，否则 worker 无法区分 token/ssh）。
func (s *DocumentService) CreateGit(ctx context.Context, actorID, kbID int64, repoURL, gitRef string, credential GitCredential, ignoreRules []string, ip string) (*DocumentItem, error) {
	repoURL = strings.TrimSpace(repoURL)
	if repoURL == "" || len(repoURL) > 1000 {
		return nil, NewServiceError(CodeValidation, "repo_url 不能为空且不超过 1000 字符")
	}
	if !strings.HasPrefix(repoURL, "http://") && !strings.HasPrefix(repoURL, "https://") &&
		!strings.HasPrefix(repoURL, "git@") && !strings.HasPrefix(repoURL, "ssh://") {
		return nil, NewServiceError(CodeValidation, "repo_url 必须是 http(s)、ssh:// 或 git@ 形式")
	}
	if gitRef != "" && len(gitRef) > 200 {
		return nil, NewServiceError(CodeValidation, "git_ref 不超过 200 字符")
	}
	if len(ignoreRules) > 50 {
		return nil, NewServiceError(CodeValidation, "ignore_rules 不超过 50 条")
	}
	cred, err := validateGitCredential(credential)
	if err != nil {
		return nil, err
	}
	if err := s.checkKBCapacity(ctx, kbID); err != nil {
		return nil, err
	}
	d := &model.Document{
		KBID: kbID, Type: model.DocTypeGit, Name: repoURL,
		RepoURL: &repoURL, IgnoreRules: model.StringArray(ignoreRules), CreatedBy: actorID,
	}
	if gitRef != "" {
		d.GitRef = &gitRef
	}
	if cred.Secret != "" {
		plain, merr := json.Marshal(cred) // {"kind","secret"}（数据库设计 §3.2）
		if merr != nil {
			return nil, fmt.Errorf("序列化仓库凭证: %w", merr)
		}
		enc, eerr := s.cipher.Encrypt(plain)
		if eerr != nil {
			return nil, eerr
		}
		d.CredentialEnc = enc
	}
	id, nerr := s.newDocAndTask(ctx, d, model.TaskTypeFull, ip, nil)
	if nerr != nil {
		return nil, nerr
	}
	return s.itemOf(d, id), nil
}

// CreateUpload 上传文件（编辑者+，FR-04）：每文件一个文档；不支持格式逐个报 DOC_002。
//
// 单文件处理顺序（审计 P1-9：先落盘 → 后入库 → 再入队）：
//  1. 校验（文件名/格式/大小/路径穿越）
//  2. 内容落盘：写到 UPLOAD_DIR 下的临时目录（写完即完整落盘，不会出现半个文件）
//  3. CreateDocument + CreateTask 同一事务
//  4. 临时目录 os.Rename 到 {uploadDir}/{kbID}/{docID}（同盘改名，原子）
//  5. Enqueue
//
// 关键点：**入队在文件就位之后**——worker 并发 4，先入队会出现「任务已取到、文件还不存在」的拉取失败。
// 任一步失败整条回滚（删文档 + 删已落盘文件），库中不留 status=待处理 的僵尸文档。
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
		item, err := s.createOneUpload(ctx, actorID, kbID, name, f.Content, ip)
		if err != nil {
			return nil, rejected, err
		}
		items = append(items, item)
	}
	return items, rejected, nil
}

// createOneUpload 单个上传文件的「先落盘 → 后入库 → 再入队」，失败整条回滚。
func (s *DocumentService) createOneUpload(ctx context.Context, actorID, kbID int64, name string, content []byte, ip string) (*DocumentItem, error) {
	// 1) 内容先落盘。用临时目录而非 {uploadDir}/{kbID}/{docID}：docID 尚未产生；
	//    临时目录与目标同盘（同在 uploadDir 下），Rename 是原子的目录改名，不会留下半个文件。
	kbDir := filepath.Join(s.uploadDir, fmt.Sprint(kbID))
	if err := os.MkdirAll(kbDir, 0o750); err != nil {
		return nil, fmt.Errorf("创建上传目录: %w", err)
	}
	staging, err := os.MkdirTemp(kbDir, ".staging-")
	if err != nil {
		return nil, fmt.Errorf("创建上传暂存目录: %w", err)
	}
	// 失败路径清理暂存目录；改名成功后该路径已不存在，RemoveAll 为无害空操作。
	defer func() { _ = os.RemoveAll(staging) }()
	if werr := os.WriteFile(filepath.Join(staging, name), content, 0o600); werr != nil {
		return nil, fmt.Errorf("写入上传文件: %w", werr)
	}

	// 2) 文档 + 任务（同一事务）→ 3) 文件改名到 {docID} 目录 → 4) 入队（见 newDocAndTask）。
	var (
		final   string
		renamed bool
	)
	d := &model.Document{KBID: kbID, Type: model.DocTypeUpload, Name: name, CreatedBy: actorID}
	id, err := s.newDocAndTask(ctx, d, model.TaskTypeFull, ip, func(docID int64) error {
		final = filepath.Join(s.uploadDir, fmt.Sprint(kbID), fmt.Sprint(docID))
		// Windows 下目标目录已存在（如上次异常落下的历史残留）时 os.Rename 报
		// "Access is denied" → 500。docID 为本次新建文档 ID，该路径不可能有合法内容，
		// 落地前先清除残留；路径不存在时 RemoveAll 返回 nil，无害。
		if rerr := os.RemoveAll(final); rerr != nil {
			return fmt.Errorf("清除上传落地目录残留: doc=%d path=%s: %w", docID, final, rerr)
		}
		if rerr := os.Rename(staging, final); rerr != nil {
			return fmt.Errorf("落地上传文件: doc=%d dst=%s src=%s: %w", docID, final, staging, rerr)
		}
		renamed = true
		return nil
	})
	if err != nil {
		// 改名成功但入队失败：文档已被回滚，落盘文件也要一并清掉（不留孤儿目录）
		if renamed {
			if rerr := os.RemoveAll(final); rerr != nil {
				slog.Error("回滚上传文件失败", "path", final, "err", rerr)
			}
		}
		return nil, err
	}
	return s.itemOf(d, id), nil
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
	id, err := s.newDocAndTask(ctx, d, model.TaskTypeFull, ip, nil)
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
		item := DocumentTaskItem{ID: t.ID, Type: taskTypeName(t.Type), Status: taskStatusName(t.Status), Retries: t.Retries, CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339)}
		if t.Error != nil {
			item.Error = *t.Error
		}
		if t.FinishedAt != nil {
			item.FinishedAt = t.FinishedAt.UTC().Format(time.RFC3339)
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
// HasRunningTask 的「先查后写」只是快速失败路径；并发下由 DB 约束
// uq_ingest_tasks_running（迁移 000006）兜底，冲突经 repo.ErrTaskRunning 映射为同一业务错误（审计 P1-10）。
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
	// 幂等：并发窗口内可能已由另一请求建了任务，唯一索引拒绝时按既有 409 语义返回
	if err := s.docs.CreateTask(ctx, t); err != nil {
		if errors.Is(err, repo.ErrTaskRunning) {
			return NewServiceError(CodeDocTaskRunning, "该文档已有运行中的摄取任务")
		}
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
		CreatedAt:    r.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:    r.UpdatedAt.UTC().Format(time.RFC3339),
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
	return t.UTC().Format(time.RFC3339)
}
