// Package worker asynq 摄取任务：拉取（R1/R1b/R2）→ 切块 → 批量向量化 → 写 chunks。
// 重试由本包自管理（R3：≤3 次间隔递增），asynq 层关闭自动重试。
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hibiken/asynq"

	"woodpecker/internal/config"
	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/rag"
	"woodpecker/internal/repo"
)

// TypeIngest asynq 任务类型名。
const TypeIngest = "wp:ingest"

// IngestPayload 任务载荷（队列里只传 task_id，状态以库为准）。
type IngestPayload struct {
	TaskID int64 `json:"task_id"`
}

// retryDelays R3 重试策略：最多 3 次，间隔递增。
var retryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// embedBatchSize 向量化批大小（技术选型：batch ≈32）。
const embedBatchSize = 32

// R2 限制（拉取时判定，与 service 保持一致）。
const (
	maxRepoBytes = 1 << 30
	maxRepoFiles = 50000
)

// Enqueuer asynq 入队抽象（asynq.Client 满足；测试用假实现）。
type Enqueuer interface {
	EnqueueContext(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

// Embedder 向量化能力（llm.Provider 满足）。
type Embedder interface {
	Embed(ctx context.Context, cfg llm.ResolvedConfig, texts []string) ([][]float32, error)
}

// ConfigResolver 默认 embedding 模型配置（service.ModelConfigService 满足）。
type ConfigResolver interface {
	GetDefault(ctx context.Context, purpose int16) (llm.ResolvedConfig, error)
}

// CredentialCipher 凭证解密（crypto.AESGCM 满足）。
type CredentialCipher interface {
	Decrypt(ciphertext []byte) ([]byte, error)
}

// DocStore 摄取所需的文档存储（repo.DocumentRepo 满足）。
type DocStore interface {
	GetTaskByID(ctx context.Context, id int64) (*model.IngestTask, error)
	SaveTask(ctx context.Context, t *model.IngestTask) error
	GetDocumentByID(ctx context.Context, id int64) (*model.Document, error)
	SaveDocument(ctx context.Context, d *model.Document) error
	UpsertFile(ctx context.Context, f *model.DocumentFile) error
	ListFiles(ctx context.Context, documentID int64) ([]model.DocumentFile, error)
	DeleteFilesByPath(ctx context.Context, documentID int64, paths []string) error
	DeleteChunksByFile(ctx context.Context, documentID int64, filePath string) error
	InsertChunks(ctx context.Context, documentID int64, chunks []model.Chunk) error
	ReplaceDocumentChunks(ctx context.Context, documentID int64, chunks []model.Chunk) error
}

// FailZombieStore 启动恢复所需操作（repo.DocumentRepo 满足）。
// 两个方法都按 worker 限定范围：多实例部署下不得全库 UPDATE（审计 P1-8）。
type FailZombieStore interface {
	// FailZombieTasks 置失败本实例（或迁移前无归属）的排队/运行任务，返回任务数与被波及文档 ID。
	FailZombieTasks(ctx context.Context, workerID string) (int64, []int64, error)
	// FailProcessingDocs 只把给定文档（本实例刚失败任务对应的）置失败。
	FailProcessingDocs(ctx context.Context, docIDs []int64) (int64, error)
}

// Pipeline 摄取管道。
type Pipeline struct {
	docs      DocStore
	settings  SettingGetter
	cipher    CredentialCipher
	resolver  ConfigResolver
	embedder  Embedder
	queue     Enqueuer
	uploadDir string
	workerID  string
	now       func() time.Time
}

// SettingGetter R1b 排除清单读取（repo.SettingRepo 满足）。
type SettingGetter interface {
	GetString(ctx context.Context, key, def string) (string, error)
}

func NewPipeline(docs DocStore, settings SettingGetter, cipher CredentialCipher, resolver ConfigResolver, embedder Embedder, queue Enqueuer) *Pipeline {
	return &Pipeline{
		docs: docs, settings: settings, cipher: cipher, resolver: resolver,
		embedder: embedder, queue: queue, uploadDir: "data/uploads",
		workerID: config.DefaultWorkerID(), now: time.Now,
	}
}

// SetUploadDir 修改上传目录（与 service.CreateUpload 的落地规则一致）。
func (p *Pipeline) SetUploadDir(dir string) { p.uploadDir = dir }

// SetWorkerID 设置本实例标识（配置 WORKER_ID，缺省主机名；审计 P1-8）。
// 空值忽略，保持缺省值——留空会让重启恢复退化为清理 worker_id IS NULL 的分支，误杀刚入队的任务。
func (p *Pipeline) SetWorkerID(id string) {
	if id = strings.TrimSpace(id); id != "" {
		p.workerID = id
	}
}

// WorkerID 当前实例标识（日志与断言用）。
func (p *Pipeline) WorkerID() string { return p.workerID }

// RecoverStartup 重启恢复：只把**本实例遗留**的运行中任务/文档置失败（FR-06）。
// 旧实现是无条件全库 UPDATE，多实例部署下任一 worker 启动都会杀掉其它实例的任务（审计 P1-8）。
func (p *Pipeline) RecoverStartup(ctx context.Context, store FailZombieStore) {
	tasks, docIDs, err := store.FailZombieTasks(ctx, p.workerID)
	if err != nil {
		slog.Error("重启恢复：遗留任务置失败", "worker_id", p.workerID, "err", err)
		return
	}
	docs, err := store.FailProcessingDocs(ctx, docIDs)
	if err != nil {
		slog.Error("重启恢复：处理中文档置失败", "worker_id", p.workerID, "err", err)
		return
	}
	if tasks+docs > 0 {
		slog.Info("重启恢复：中断任务按失败处理",
			"worker_id", p.workerID, "docs", docs, "tasks", tasks)
	}
}

// Process 处理一次摄取任务。
// 返回 nil：任务终结（成功/最终失败/已排重试）；返回 error：临时故障（asynq 保留任务，下次重投）。
func (p *Pipeline) Process(ctx context.Context, taskID int64) error {
	t, err := p.docs.GetTaskByID(ctx, taskID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			slog.Warn("摄取任务不存在，跳过", "task_id", taskID)
			return nil
		}
		return err
	}
	if t.Status != model.TaskStatusQueued {
		return nil // 已被处理或已取消
	}
	now := p.now()
	t.Status = model.TaskStatusRunning
	t.StartedAt = &now
	// 归属标记（审计 P1-8）：重启恢复据此只清理本实例的残留，不再全库 UPDATE。
	// 与状态同一次 SaveTask 落库，避免「running 但无归属」的中间态被其它实例当成历史数据清掉。
	if p.workerID != "" {
		workerID := p.workerID
		t.WorkerID = &workerID
	}
	if err := p.docs.SaveTask(ctx, t); err != nil {
		return err
	}

	d, err := p.docs.GetDocumentByID(ctx, t.DocumentID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return p.finishTask(ctx, t, model.TaskStatusFailed, "文档已删除") // 删除处理中文档即中止任务
		}
		return err
	}

	// ---- 解析（stage 1）----
	p.setStage(ctx, d, model.DocStageParse)
	res, err := p.fetch(ctx, d, t)
	if err != nil {
		return p.fail(ctx, t, d, err)
	}

	// ---- 切块 + 文件清单（stage 2）----
	p.setStage(ctx, d, model.DocStageChunk)
	isSync := t.Type == model.TaskTypeSync
	groups, fileCount, totalSize, err := p.buildChunks(ctx, d, res, isSync)
	if err != nil {
		return p.fail(ctx, t, d, err)
	}

	// ---- 向量化 + 入库（stage 3）----
	p.setStage(ctx, d, model.DocStageEmbed)
	if err := p.embedAndStore(ctx, d, groups, isSync); err != nil {
		return p.fail(ctx, t, d, err)
	}

	// ---- 完成 ----
	d.FileCount = &fileCount
	d.TotalSize = &totalSize
	if d.Type == model.DocTypeGit && res.Commit != "" {
		d.LastCommit = &res.Commit
		synced := p.now()
		d.LastSyncedAt = &synced
	}
	d.Status = model.DocStatusReady
	d.Stage = nil
	d.Error = nil
	d.UpdatedAt = p.now()
	if err := p.docs.SaveDocument(ctx, d); err != nil {
		return err
	}
	return p.finishTask(ctx, t, model.TaskStatusSuccess, "")
}

// fetch 按文档类型拉取内容（R1/R1b/R2 过滤在 rag 层完成）。
func (p *Pipeline) fetch(ctx context.Context, d *model.Document, _ *model.IngestTask) (*rag.FetchResult, error) {
	raw := ""
	if p.settings != nil {
		raw, _ = p.settings.GetString(ctx, "exclusion_rules", "")
	}
	excl := rag.ParseExclusionRules(raw)
	ignore := d.IgnoreRules

	switch d.Type {
	case model.DocTypeGit:
		cred := rag.Credential{}
		if len(d.CredentialEnc) > 0 {
			plain, derr := p.cipher.Decrypt(d.CredentialEnc)
			if derr != nil {
				return nil, fmt.Errorf("解密仓库凭证: %w", derr)
			}
			// 新数据为 {"kind","secret"}；旧数据为裸 token 字符串 → 按 token 兼容（审计 P1-12）
			cred = rag.ParseCredential(plain)
		}
		ref := ""
		if d.GitRef != nil {
			ref = *d.GitRef
		}
		if d.RepoURL == nil {
			return nil, errors.New("缺少仓库地址")
		}
		return rag.FetchGit(ctx, *d.RepoURL, ref, cred, maxRepoBytes, maxRepoFiles, ignore, excl)

	case model.DocTypeUpload:
		return rag.FetchLocalDir(ctx, p.uploadPath(d), maxRepoBytes, maxRepoFiles, ignore, excl)

	case model.DocTypeURL:
		if d.SourceURL == nil {
			return nil, errors.New("缺少源 URL")
		}
		body, title, err := rag.FetchURL(ctx, *d.SourceURL)
		if err != nil {
			return nil, err
		}
		name := title
		if name == "" {
			name = *d.SourceURL
		}
		res := &rag.FetchResult{}
		if rag.IsExcluded(name, ignore, excl) {
			res.Excluded = append(res.Excluded, name)
		} else {
			res.Files = []rag.SourceFile{{Path: name, Content: body}}
			res.TotalSize = int64(len(body))
			res.FileSeen = 1
		}
		return res, nil
	}
	return nil, fmt.Errorf("未知文档类型 %d", d.Type)
}

// uploadPath 上传文档的落地目录（与 service.CreateUpload 一致）。
func (p *Pipeline) uploadPath(d *model.Document) string {
	return fmt.Sprintf("%s/%d/%d", p.uploadDir, d.KBID, d.ID)
}

// chunkGroup 本轮产出的「单文件分块」。增量同步按文件替换，故必须保留文件边界；
// 文件变更后切不出块时仍会返回空组，用于清掉该文件的旧块。
type chunkGroup struct {
	filePath string
	chunks   []model.Chunk
}

// buildChunks 切块并维护文件清单；返回按文件分组、待向量化的分块。
// isSync=true 时做文件级增量：hash 未变的文件跳过切块与向量化；远端已删除的清理分块与清单。
// 注意：本函数**不删除**变更文件的旧分块——旧块要等新块向量化成功后才删（见 embedAndStore）。
func (p *Pipeline) buildChunks(ctx context.Context, d *model.Document, res *rag.FetchResult, isSync bool) (groups []chunkGroup, fileCount int, totalSize int64, err error) {
	existing := map[string]model.DocumentFile{}
	if isSync {
		files, err := p.docs.ListFiles(ctx, d.ID)
		if err != nil {
			return nil, 0, 0, err
		}
		for _, f := range files {
			existing[f.Path] = f
		}
	}

	// 远端已删除：清分块 + 清清单（内容已不存在，无需等向量化）
	if isSync {
		var removed []string
		for fp := range existing {
			if _, ok := fetchPath(res, fp); !ok {
				removed = append(removed, fp)
			}
		}
		if len(removed) > 0 {
			if err := p.docs.DeleteFilesByPath(ctx, d.ID, removed); err != nil {
				return nil, 0, 0, err
			}
			for _, fp := range removed {
				if err := p.docs.DeleteChunksByFile(ctx, d.ID, fp); err != nil {
					return nil, 0, 0, err
				}
			}
		}
	}

	groups = nil
	fileCount = len(res.Files) + len(res.Excluded) + len(res.Oversized)
	totalSize = 0

	// 排除文件 → "已排除"标记
	for _, fp := range res.Excluded {
		f := &model.DocumentFile{DocumentID: d.ID, Path: fp, Status: model.FileStatusExcluded, UpdatedAt: p.now()}
		if err := p.docs.UpsertFile(ctx, f); err != nil {
			return nil, 0, 0, err
		}
	}
	// 超大文件 → "失败"标记
	for _, fp := range res.Oversized {
		msg := "超过单文件 5MB 上限"
		f := &model.DocumentFile{DocumentID: d.ID, Path: fp, Status: model.FileStatusFailed, Error: &msg, UpdatedAt: p.now()}
		if err := p.docs.UpsertFile(ctx, f); err != nil {
			return nil, 0, 0, err
		}
	}

	// 受支持文件：切块
	for _, sf := range res.Files {
		totalSize += int64(len(sf.Content))
		hash := contentHash(sf.Content)
		if old, ok := existing[sf.Path]; ok && old.ContentHash != nil && *old.ContentHash == hash {
			// 未变更：跳过切块与向量化（同步只处理变更文件；其既有分块保持不动）
			continue
		}
		f := &model.DocumentFile{
			DocumentID: d.ID, Path: sf.Path, Status: model.FileStatusIndexed,
			Language: langPtr(sf.Path), Size: sizePtr(int64(len(sf.Content))),
			ContentHash: &hash, UpdatedAt: p.now(),
		}
		if err := p.docs.UpsertFile(ctx, f); err != nil {
			return nil, 0, 0, err
		}
		g := chunkGroup{filePath: sf.Path}
		for i, b := range rag.Chunk(rag.ContentKind(sf.Path), string(sf.Content)) {
			// chunk_index 为**文件内序号**（唯一键 UNIQUE(document_id, file_path, chunk_index)）
			g.chunks = append(g.chunks, makeChunk(d, f.ID, sf.Path, b, i))
		}
		groups = append(groups, g)
	}
	return groups, fileCount, totalSize, nil
}

// makeChunk 组装单块（标题路径前缀进内容，增强检索上下文）。
// index 为**文件内序号**（与迁移 000003 的 UNIQUE(document_id, file_path, chunk_index) 对应）。
func makeChunk(d *model.Document, fileID int64, filePath string, b rag.ChunkBlock, index int) model.Chunk {
	content := b.Content
	hp := b.HeadingPath
	if hp != "" {
		content = hp + "\n" + content
	} else {
		hp = ""
	}
	tc := utf8.RuneCountInString(content)
	ls, le := b.LineStart, b.LineEnd
	c := model.Chunk{
		DocumentID: d.ID, KBID: d.KBID,
		Content: content, ChunkIndex: index,
		TokenCount: &tc, FilePath: &filePath,
		LineStart: &ls, LineEnd: &le,
	}
	if fileID > 0 {
		c.FileID = &fileID
	}
	if hp != "" {
		c.HeadingPath = &hp
	}
	return c
}

// embedAndStore 批量向量化后入库。
//   - 全量摄取：整篇替换（ReplaceDocumentChunks，事务内先删后插，重建期间读请求只看旧快照）。
//   - 增量同步：逐文件「先 embed 成功 → 再删旧块 → 插新块」，未变更文件的分块完全不动（P0-1）；
//     顺序很关键：embedding 失败时必须保留旧块，否则「同步期间问答使用旧内容不受影响」不成立。
func (p *Pipeline) embedAndStore(ctx context.Context, d *model.Document, groups []chunkGroup, isSync bool) error {
	// flat 指向各组底层的元素，向量回填无需二次拷贝
	var flat []*model.Chunk
	for gi := range groups {
		for ci := range groups[gi].chunks {
			flat = append(flat, &groups[gi].chunks[ci])
		}
	}
	if len(flat) == 0 {
		if isSync {
			return nil // 本轮无变更文件：既有分块原样保留（旧实现在此整篇清空）
		}
		return p.docs.ReplaceDocumentChunks(ctx, d.ID, nil)
	}
	cfg, err := p.resolver.GetDefault(ctx, model.ModelPurposeEmbedding)
	if err != nil {
		return fmt.Errorf("获取默认向量化模型: %w", err)
	}
	for start := 0; start < len(flat); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(flat) {
			end = len(flat)
		}
		texts := make([]string, 0, end-start)
		for _, c := range flat[start:end] {
			texts = append(texts, c.Content)
		}
		vecs, err := p.embedder.Embed(ctx, cfg, texts)
		if err != nil {
			return fmt.Errorf("向量化: %w", err)
		}
		if len(vecs) != len(texts) {
			return fmt.Errorf("向量化返回数量不符: 期望 %d 实际 %d", len(texts), len(vecs))
		}
		for i, v := range vecs {
			flat[start+i].Embedding = v
		}
	}
	if isSync {
		for _, g := range groups {
			if err := p.docs.DeleteChunksByFile(ctx, d.ID, g.filePath); err != nil {
				return err
			}
			if err := p.docs.InsertChunks(ctx, d.ID, g.chunks); err != nil {
				return err
			}
		}
		return nil
	}
	all := make([]model.Chunk, 0, len(flat))
	for _, g := range groups {
		all = append(all, g.chunks...)
	}
	return p.docs.ReplaceDocumentChunks(ctx, d.ID, all)
}

// setStage 更新处理阶段。
func (p *Pipeline) setStage(ctx context.Context, d *model.Document, stage int16) {
	d.Status = model.DocStatusProcessing
	d.Stage = &stage
	d.UpdatedAt = p.now()
	if err := p.docs.SaveDocument(ctx, d); err != nil {
		slog.Error("更新文档阶段失败", "doc", d.ID, "stage", stage, "err", err)
	}
}

// fail 单次失败：重试未达上限 → 复位任务状态并按递增间隔重排；达到上限 → 文档与任务置失败（R3）。
func (p *Pipeline) fail(ctx context.Context, t *model.IngestTask, d *model.Document, cause error) error {
	msg := classifyFail(cause)
	if t.Retries < len(retryDelays) && p.queue != nil {
		t.Retries++
		t.Status = model.TaskStatusQueued // 重排后等待下一次投递重新进入 running
		if err := p.docs.SaveTask(ctx, t); err != nil {
			return err
		}
		delay := retryDelays[t.Retries-1]
		payload, _ := json.Marshal(IngestPayload{TaskID: t.ID})
		if _, qerr := p.queue.EnqueueContext(ctx, asynq.NewTask(TypeIngest, payload, asynq.MaxRetry(0), asynq.ProcessIn(delay))); qerr != nil {
			slog.Error("重试入队失败，按最终失败处理", "task", t.ID, "err", qerr)
		} else {
			slog.Warn("摄取失败，已排重试", "task", t.ID, "doc", d.ID, "retries", t.Retries, "delay", delay.String(), "err", msg)
			return nil
		}
	}
	d.Status = model.DocStatusFailed
	d.Stage = nil
	d.Error = &msg
	d.UpdatedAt = p.now()
	if err := p.docs.SaveDocument(ctx, d); err != nil {
		slog.Error("置文档失败态出错", "doc", d.ID, "err", err)
	}
	return p.finishTask(ctx, t, model.TaskStatusFailed, msg)
}

// finishTask 收尾任务状态。
// 任务行已被级联删除（文档在处理中被删）时视为终结，不再返回临时错误——
// 此时没有可写的任务行，交给 asynq 重投只会重复失败。
func (p *Pipeline) finishTask(ctx context.Context, t *model.IngestTask, status int16, errMsg string) error {
	t.Status = status
	now := p.now()
	t.FinishedAt = &now
	if errMsg != "" {
		t.Error = &errMsg
	}
	if err := p.docs.SaveTask(ctx, t); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			slog.Warn("任务已随文档级联删除，跳过收尾", "task", t.ID)
			return nil
		}
		return err
	}
	return nil
}

// classifyFail 归类错误为对外可见的失败摘要。
func classifyFail(err error) string {
	switch {
	case errors.Is(err, rag.ErrRepoTooLarge):
		return "仓库超过大小或文件数限制（DOC_001）"
	case errors.Is(err, rag.ErrRepoUnreachable):
		return "仓库地址不可达或凭证无效（DOC_003）"
	case errors.Is(err, rag.ErrURLUnreachable):
		return "URL 无法访问（DOC_003）"
	case errors.Is(err, rag.ErrURLNotHTML):
		return "URL 内容不是 HTML 页面（DOC_002）"
	default:
		return strings.TrimSpace(err.Error())
	}
}

func contentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func langPtr(p string) *string {
	l := rag.DetectLanguage(p)
	return &l
}

func sizePtr(s int64) *int64 { return &s }

// fetchPath 查找拉取结果中的路径。
func fetchPath(res *rag.FetchResult, path string) (rag.SourceFile, bool) {
	for _, f := range res.Files {
		if f.Path == path {
			return f, true
		}
	}
	return rag.SourceFile{}, false
}
