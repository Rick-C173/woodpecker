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
	"sync"
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
	ListFiles(ctx context.Context, documentID int64) ([]model.DocumentFile, error)
	// 带 fencing 的写入（审查 #1/#5 补强）：事务内校验任务仍 running 才写，
	// 任务被 sweeper 回收后拒绝落库，避免与新任务并发写同一文档。
	IsTaskRunning(ctx context.Context, taskID int64) (bool, error)
	ReplaceFileChunksIfTaskRunning(ctx context.Context, taskID, documentID int64, filePath string, file *model.DocumentFile, chunks []model.Chunk) (bool, error)
	ReplaceDocumentChunksIfTaskRunning(ctx context.Context, taskID, documentID int64, files []*model.DocumentFile, chunks []model.Chunk) (bool, error)
	// MaintainFilesIfTaskRunning 带 fencing 的清单维护：删除远端已删文件及其分块、写入排除/超大标记。
	MaintainFilesIfTaskRunning(ctx context.Context, taskID, documentID int64, removePaths []string, files []*model.DocumentFile) (bool, error)
	// UpdateDocumentIfTaskRunning 带 fencing 的文档状态写入（阶段推进）。
	UpdateDocumentIfTaskRunning(ctx context.Context, taskID, documentID int64, updates map[string]any) (bool, error)
	// FinishTaskIfRunning 原子收尾任务终态与文档终态。
	FinishTaskIfRunning(ctx context.Context, taskID, documentID int64, taskUpdates, docUpdates map[string]any) (bool, error)
	// HeartbeatTask 刷新运行中任务的心跳时刻（验收回归 8-1：跨实例僵尸任务的时间兜底依据）。
	HeartbeatTask(ctx context.Context, taskID int64) error
	// TransitionTaskIfRunning 带守卫的状态流转：仅当任务仍为 running 时应用 updates
	// （审查 #1：任务被 sweeper 回收后，原 worker 的收尾不得覆写其失败判定）。
	TransitionTaskIfRunning(ctx context.Context, taskID int64, updates map[string]any) (bool, error)
	// FailStaleTasks 周期性僵尸回收：只按心跳时间判定，**不带归属条件**（否则会杀掉本实例
	// 正在处理的任务）。返回任务数与被波及文档 ID。
	FailStaleTasks(ctx context.Context, timeoutMinutes int) (int64, []int64, error)
	// FailProcessingDocs 只把给定文档中仍是处理中的置失败。
	FailProcessingDocs(ctx context.Context, docIDs []int64) (int64, error)
}

// FailZombieStore 启动恢复所需操作（repo.DocumentRepo 满足）。
// 两个方法都按 worker 限定范围：多实例部署下不得全库 UPDATE（审计 P1-8）。
type FailZombieStore interface {
	// FailZombieTasks 置失败本实例遗留的运行中任务，以及心跳超过 timeoutMinutes 未更新的运行中任务；
	// 排队任务（status=1）无论归属如何都不动（asynq 会重投）。返回任务数与被波及文档 ID。
	// timeoutMinutes 为心跳超时阈值（验收回归 8-1：WORKER_ID 跨重启变化时按归属匹配不到，靠时间兜底）。
	FailZombieTasks(ctx context.Context, workerID string, timeoutMinutes int) (int64, []int64, error)
	// FailProcessingDocs 只把给定文档（本实例刚失败任务对应的）置失败。
	FailProcessingDocs(ctx context.Context, docIDs []int64) (int64, error)
}

// heartbeatIntervalDefault 任务心跳间隔缺省值（验收回归 8-1）。
// 必须是 WorkerZombieTimeoutMinutes 的 1/5 以下，否则正常任务会被时间兜底误判为僵尸。
const heartbeatIntervalDefault = 30 * time.Second

// heartbeatInterval 心跳实际间隔（包级变量便于测试注入，生产恒为 heartbeatIntervalDefault）。
var heartbeatInterval = heartbeatIntervalDefault

// sweeperIntervalDefault 周期性僵尸回收间隔缺省值（配置 WORKER_ZOMBIE_SWEEP_MINUTES，单位分钟）。
// 需大于心跳间隔（30s），否则同一批任务会被反复判定；2 分钟 = 4 个心跳周期，足够稳。
const sweeperIntervalDefault = 2 * time.Minute

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
	// zombieTimeoutMinutes 僵尸任务心跳超时阈值（分钟，验收回归 8-1），来自配置。
	zombieTimeoutMinutes int
	// sweepInterval 周期性僵尸回收间隔（验收回归 8-1 补强）；<=0 表示不启动 sweeper。
	sweepInterval time.Duration
	now           func() time.Time
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
		zombieTimeoutMinutes: config.DefaultWorkerZombieTimeoutMinutes,
		sweepInterval:        sweeperIntervalDefault,
	}
}

// SetZombieTimeoutMinutes 设置僵尸任务心跳超时阈值（分钟，验收回归 8-1）。
// <=0 忽略，保持缺省值——阈值配成 0 会让「任何心跳都算超时」，把活任务当僵尸杀掉。
func (p *Pipeline) SetZombieTimeoutMinutes(minutes int) {
	if minutes > 0 {
		p.zombieTimeoutMinutes = minutes
	}
}

// SetZombieSweepMinutes 设置周期性僵尸回收间隔（分钟，验收回归 8-1 补强）。
// 缺省 2 分钟；<=0 关闭 sweeper（RunSweeper 立即返回），此时心跳超时的僵尸任务只能靠
// worker 重启时的 RecoverStartup 回收——Pod 秒级重建 + 长期不重启的场景会再次卡死，慎用。
func (p *Pipeline) SetZombieSweepMinutes(minutes int) {
	if minutes > 0 {
		p.sweepInterval = time.Duration(minutes) * time.Minute
	}
}

// SetZombieSweepInterval 直接设置回收间隔（测试注入用；生产走 SetZombieSweepMinutes）。
func (p *Pipeline) SetZombieSweepInterval(d time.Duration) { p.sweepInterval = d }

// SweepZombies 回收一次心跳超时的僵尸任务（验收回归 8-1 补强）。
// 只调 FailStaleTasks（无归属条件），故不会杀掉本实例正在处理的任务；
// 随后用 docIDs 限定把对应文档置失败（不做全库 UPDATE）。
func (p *Pipeline) SweepZombies(ctx context.Context) (int64, error) {
	tasks, docIDs, err := p.docs.FailStaleTasks(ctx, p.zombieTimeoutMinutes)
	if err != nil {
		return 0, err
	}
	if tasks == 0 {
		return 0, nil
	}
	docs, derr := p.docs.FailProcessingDocs(ctx, docIDs)
	if derr != nil {
		// 任务已置失败，文档置失败失败可重试：下次 sweeper 不会再命中（任务已非 running），
		// 故这里显式补一次日志，便于排障；返回错误交由调用方记录。
		slog.Warn("回收僵尸任务：文档置失败出错", "tasks", tasks, "docs", docIDs, "err", derr)
		return tasks, derr
	}
	slog.Info("回收僵尸任务", "tasks", tasks, "docs", docs, "timeout_minutes", p.zombieTimeoutMinutes)
	return tasks, nil
}

// RunSweeper 周期性回收心跳超时的僵尸任务，直到 ctx 结束（阻塞，调用方用 go 启动）。
// 覆盖「Pod 秒级重建」场景：崩溃后新 Pod 立刻起来时心跳尚未过期，启动恢复看不到陈旧任务，
// 由本循环在心跳过期后的第一个周期内回收，无需等到下一次重启。
func (p *Pipeline) RunSweeper(ctx context.Context) {
	if p.sweepInterval <= 0 {
		slog.Warn("周期性僵尸回收未启动（间隔 <=0）：心跳超时的僵尸任务只能靠 worker 重启回收")
		return
	}
	slog.Info("周期性僵尸回收已启动", "interval", p.sweepInterval.String())
	ticker := time.NewTicker(p.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := p.SweepZombies(ctx); err != nil {
				slog.Warn("周期性僵尸回收失败", "err", err)
			}
		}
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

// RecoverStartup 重启恢复：把**本实例遗留**的运行中任务/文档置失败（FR-06），
// 以及心跳超时（zombieTimeoutMinutes）的跨实例僵尸任务置失败（验收回归 8-1）。
// 旧实现是无条件全库 UPDATE，多实例部署下任一 worker 启动都会杀掉其它实例的任务（审计 P1-8）；
// 排队中（status=1）的任务无论归属如何都不清理——asynq 队列持久化，重启后会重投执行。
func (p *Pipeline) RecoverStartup(ctx context.Context, store FailZombieStore) {
	tasks, docIDs, err := store.FailZombieTasks(ctx, p.workerID, p.zombieTimeoutMinutes)
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

// heartbeatLoop 周期性刷新任务心跳，直到 ctx 结束（由 Process 的 defer 取消）。
// 进入循环先立即写一次：否则刚 running 的任务在第一个心跳周期内没有 heartbeat_at，
// 会被时间兜底按 started_at 判定（大任务若启动即慢，仍不会被误杀，但立即写更稳妥）。
func (p *Pipeline) heartbeatLoop(ctx context.Context, taskID int64) {
	beat := func() {
		if err := p.docs.HeartbeatTask(ctx, taskID); err != nil {
			slog.Warn("刷新摄取任务心跳失败", "task", taskID, "err", err)
		}
	}
	beat()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beat()
		}
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

	// 心跳（验收回归 8-1）：任务进入 running 后周期性刷新 heartbeat_at，供其它实例按「时间兜底」
	// 判定本任务是否还活着——WORKER_ID 跨重启变化（k8s 重建 Pod → 主机名必变）时按归属匹配不到，
	// 只能靠心跳时间识别僵尸。WithoutCancel：请求 ctx 被取消也要能落心跳；defer + WaitGroup
	// 保证 Process 返回前心跳 goroutine 已退出，不留泄漏。
	hbCtx, hbCancel := context.WithCancel(context.WithoutCancel(ctx))
	var hbWG sync.WaitGroup
	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		p.heartbeatLoop(hbCtx, t.ID)
	}()
	defer func() {
		hbCancel()
		hbWG.Wait()
	}()

	d, err := p.docs.GetDocumentByID(ctx, t.DocumentID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return p.finishTask(ctx, t, model.TaskStatusFailed, "文档已删除") // 删除处理中文档即中止任务
		}
		return err
	}

	// ---- 解析（stage 1）----
	if err := p.markStage(ctx, t, d, model.DocStageParse); err != nil {
		return err
	}
	res, err := p.fetch(ctx, d, t)
	if err != nil {
		return p.fail(ctx, t, d, err)
	}

	// ---- 切块 + 文件清单（stage 2）----
	if err := p.markStage(ctx, t, d, model.DocStageChunk); err != nil {
		return err
	}
	isSync := t.Type == model.TaskTypeSync
	groups, fileCount, totalSize, err := p.buildChunks(ctx, t.ID, d, res, isSync)
	if err != nil {
		if errors.Is(err, ErrTaskFenced) {
			return p.abandonFenced(t, d)
		}
		return p.fail(ctx, t, d, err)
	}

	// ---- 向量化 + 入库（stage 3）----
	if err := p.markStage(ctx, t, d, model.DocStageEmbed); err != nil {
		return err
	}
	if err := p.embedAndStore(ctx, t.ID, d, groups, isSync); err != nil {
		if errors.Is(err, ErrTaskFenced) {
			return p.abandonFenced(t, d)
		}
		return p.fail(ctx, t, d, err)
	}

	// ---- 完成（审查 #5：任务终态与文档终态同事务，防止僵尸回收后互相覆盖）----
	docUpdates := map[string]any{
		"status":     model.DocStatusReady,
		"stage":      nil,
		"error":      nil,
		"file_count": fileCount,
		"total_size": totalSize,
		"updated_at": p.now(),
	}
	if d.Type == model.DocTypeGit && res.Commit != "" {
		docUpdates["last_commit"] = res.Commit
		docUpdates["last_synced_at"] = p.now()
	}
	applied, err := p.docs.FinishTaskIfRunning(ctx, t.ID, d.ID,
		map[string]any{"status": model.TaskStatusSuccess, "finished_at": p.now()}, docUpdates)
	if err != nil {
		return err
	}
	if !applied {
		return p.abandonFenced(t, d)
	}
	return nil
}

// fetch 按文档类型拉取内容（R1/R1b/R2 过滤在 rag 层完成）。
func (p *Pipeline) fetch(ctx context.Context, d *model.Document, _ *model.IngestTask) (*rag.FetchResult, error) {
	raw := ""
	if p.settings != nil {
		raw, _ = p.settings.GetString(ctx, "exclusion_rules", "")
	}
	excl := rag.ParseExclusionRules(raw)
	ignore := []string(d.IgnoreRules)

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
// ErrTaskFenced 任务已被僵尸回收/终结，本次写入必须放弃（由接管的新任务负责）。
// 不视为失败：asynq 不应重投——重投只会再次被 fencing 拒绝。
var ErrTaskFenced = errors.New("任务已被先行终结，放弃写入")

type chunkGroup struct {
	filePath string
	// meta 文件清单元数据（含 content_hash），必须与 chunks 在同一事务内落库（审查 #1 补强）。
	// 不能在切块阶段单独 UpsertFile：hash 先落库会让新任务跳过该文件，而它的 chunks 可能没写，
	// 文档会永久缺块。
	meta   *model.DocumentFile
	chunks []model.Chunk
}

// buildChunks 切块并维护文件清单；返回按文件分组、待向量化的分块。
// isSync=true 时做文件级增量：hash 未变的文件跳过切块与向量化；远端已删除的清理分块与清单。
// 注意：本函数**不删除**变更文件的旧分块——旧块要等新块向量化成功后才删（见 embedAndStore）。
// 受支持文件的清单元数据**不在此处落库**：必须与 chunks 在同一事务内写入（fencing，审查 #1 补强）。
func (p *Pipeline) buildChunks(ctx context.Context, taskID int64, d *model.Document, res *rag.FetchResult, isSync bool) (groups []chunkGroup, fileCount int, totalSize int64, err error) {
	// 快速失败：任务已被回收时不必再做切块（严格并发保护由写入事务内的行锁提供）。
	if ok, err := p.docs.IsTaskRunning(ctx, taskID); err != nil {
		return nil, 0, 0, err
	} else if !ok {
		return nil, 0, 0, ErrTaskFenced
	}
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
	var removed []string
	if isSync {
		for fp := range existing {
			if _, ok := fetchPath(res, fp); !ok {
				removed = append(removed, fp)
			}
		}
	}

	groups = nil
	fileCount = len(res.Files) + len(res.Excluded) + len(res.Oversized)
	totalSize = 0

	// 排除文件 → "已排除"标记
	marks := make([]*model.DocumentFile, 0, len(res.Excluded)+len(res.Oversized))
	for _, fp := range res.Excluded {
		marks = append(marks, &model.DocumentFile{
			DocumentID: d.ID, Path: fp, Status: model.FileStatusExcluded, UpdatedAt: p.now(),
		})
	}
	// 超大文件 → "失败"标记
	for _, fp := range res.Oversized {
		msg := "超过单文件 5MB 上限"
		marks = append(marks, &model.DocumentFile{
			DocumentID: d.ID, Path: fp, Status: model.FileStatusFailed, Error: &msg, UpdatedAt: p.now(),
		})
	}
	// 删除与标记必须与分块写入同样受 fencing 保护（审查 #5）：
	// 否则被回收的僵尸 worker 仍能删掉新任务刚写入的文件与分块。
	if len(removed) > 0 || len(marks) > 0 {
		applied, err := p.docs.MaintainFilesIfTaskRunning(ctx, taskID, d.ID, removed, marks)
		if err != nil {
			return nil, 0, 0, err
		}
		if !applied {
			return nil, 0, 0, ErrTaskFenced
		}
	}

	// 受支持文件：切块（清单元数据随分块在写入事务内落库，避免 hash 先行导致新任务跳过该文件）
	for _, sf := range res.Files {
		totalSize += int64(len(sf.Content))
		hash := contentHash(sf.Content)
		if old, ok := existing[sf.Path]; ok && old.ContentHash != nil && *old.ContentHash == hash {
			// 未变更：跳过切块与向量化（同步只处理变更文件；其既有分块保持不动）
			continue
		}
		g := chunkGroup{
			filePath: sf.Path,
			meta: &model.DocumentFile{
				DocumentID: d.ID, Path: sf.Path, Status: model.FileStatusIndexed,
				Language: langPtr(sf.Path), Size: sizePtr(int64(len(sf.Content))),
				ContentHash: &hash, UpdatedAt: p.now(),
			},
		}
		for i, b := range rag.Chunk(rag.ContentKind(sf.Path), string(sf.Content)) {
			// chunk_index 为**文件内序号**（唯一键 UNIQUE(document_id, file_path, chunk_index)）；
			// file_id 由写入事务 upsert 后回填（切块阶段还没有文件 ID）
			g.chunks = append(g.chunks, makeChunk(d, 0, sf.Path, b, i))
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
//   - 增量同步且本轮无任何待向量化块：只要有变更文件（groups 非空）就仍按其路径删旧块
//     （变更后切不出块的文件必须清空旧块），groups 为空才是「本轮无变更文件」。
func (p *Pipeline) embedAndStore(ctx context.Context, taskID int64, d *model.Document, groups []chunkGroup, isSync bool) error {
	// flat 指向各组底层的元素，向量回填无需二次拷贝
	var flat []*model.Chunk
	for gi := range groups {
		for ci := range groups[gi].chunks {
			flat = append(flat, &groups[gi].chunks[ci])
		}
	}
	// 两条写入路径都带 fencing（审查 #1 补强）：事务内校验任务仍 running，
	// 文件元数据与分块同事务落库；任务已被回收时 applied=false，立即中止交给接管者。
	writeSync := func() error {
		for _, g := range groups {
			applied, err := p.docs.ReplaceFileChunksIfTaskRunning(ctx, taskID, d.ID, g.filePath, g.meta, g.chunks)
			if err != nil {
				return err
			}
			if !applied {
				return ErrTaskFenced
			}
		}
		return nil
	}
	writeFull := func(chunks []model.Chunk) error {
		files := make([]*model.DocumentFile, 0, len(groups))
		for _, g := range groups {
			if g.meta != nil {
				files = append(files, g.meta)
			}
		}
		applied, err := p.docs.ReplaceDocumentChunksIfTaskRunning(ctx, taskID, d.ID, files, chunks)
		if err != nil {
			return err
		}
		if !applied {
			return ErrTaskFenced
		}
		return nil
	}
	if len(flat) == 0 {
		if isSync {
			// 本轮没有需要向量化的块，但 groups 非空说明有「变更后切不出块」的文件（典型：内容被清空）。
			// 必须在该文件的事务里同步更新 hash 并删旧块，否则已删除的内容永久可被检索命中（8-2）。
			return writeSync()
		}
		return writeFull(nil)
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
		return writeSync()
	}
	all := make([]model.Chunk, 0, len(flat))
	for _, g := range groups {
		all = append(all, g.chunks...)
	}
	return writeFull(all)
}

// markStage 带 fencing 的阶段推进（审查 #5）。
// 任务已被回收时中止处理；其它数据库错误只记日志、不中断，与既有行为一致。
func (p *Pipeline) markStage(ctx context.Context, t *model.IngestTask, d *model.Document, stage int16) error {
	if err := p.setStage(ctx, t.ID, d, stage); err != nil {
		if errors.Is(err, ErrTaskFenced) {
			return p.abandonFenced(t, d)
		}
		slog.Error("更新文档阶段失败", "doc", d.ID, "stage", stage, "err", err)
	}
	return nil
}

// setStage 更新处理阶段（事务内校验任务仍 running）。
// 不这样做的话，sweeper 已把文档置 failed 后，僵尸 worker 仍会把它覆盖回 processing，
// 随后被写入 fencing 拒绝并放弃，文档将永久停在 processing 且无法 retry（审查 #5）。
func (p *Pipeline) setStage(ctx context.Context, taskID int64, d *model.Document, stage int16) error {
	now := p.now()
	applied, err := p.docs.UpdateDocumentIfTaskRunning(ctx, taskID, d.ID, map[string]any{
		"status": model.DocStatusProcessing, "stage": stage, "updated_at": now,
	})
	if err != nil {
		return err
	}
	if !applied {
		return ErrTaskFenced
	}
	d.Status = model.DocStatusProcessing
	d.Stage = &stage
	d.UpdatedAt = now
	return nil
}

// fail 单次失败：重试未达上限 → 复位任务状态并按递增间隔重排；达到上限 → 文档与任务置失败（R3）。
func (p *Pipeline) fail(ctx context.Context, t *model.IngestTask, d *model.Document, cause error) error {
	msg := classifyFail(cause)
	if t.Retries < len(retryDelays) && p.queue != nil {
		t.Retries++
		t.Status = model.TaskStatusQueued // 重排后等待下一次投递重新进入 running
		// 审查 #1：与 finishTask 同一守卫——任务已被 sweeper 回收（failed）时不得把它写回
		// queued（否则僵尸任务重新占用唯一索引、与回收判定打架）。
		requeued, err := p.docs.TransitionTaskIfRunning(ctx, t.ID, map[string]any{
			"status": model.TaskStatusQueued, "retries": t.Retries,
		})
		if err != nil {
			return err
		}
		if !requeued {
			slog.Warn("任务已被先行终结（僵尸回收），放弃重试重排", "task", t.ID)
			return nil
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
	// 审查 #5：文档失败态与任务失败态同事务落库。任务已被 sweeper 回收时两者都不写——
	// 否则旧 worker 会把接管方（新任务）刚写入的文档状态覆盖回 failed。
	applied, err := p.docs.FinishTaskIfRunning(ctx, t.ID, d.ID,
		map[string]any{"status": model.TaskStatusFailed, "finished_at": p.now(), "error": msg},
		map[string]any{
			"status": model.DocStatusFailed, "stage": nil, "error": msg, "updated_at": p.now(),
		})
	if err != nil {
		slog.Error("置文档失败态出错", "doc", d.ID, "err", err)
		return err
	}
	if !applied {
		slog.Warn("任务已被先行终结（僵尸回收/级联删除），放弃收尾覆写",
			"task", t.ID, "intended_status", model.TaskStatusFailed)
	}
	return nil
}

// finishTask 收尾任务状态（审查 #1：CAS 守卫）。
// 仅当任务仍为 running 时写终态——任务已被 sweeper 回收（僵尸判定）或随文档级联删除时，
// 放弃覆写并视为终结，不返回临时错误：交给 asynq 重投只会重复失败，覆写失败态会与
// 回收判定打架并给 retry 让出双跑窗口。
func (p *Pipeline) finishTask(ctx context.Context, t *model.IngestTask, status int16, errMsg string) error {
	updates := map[string]any{"status": status, "finished_at": p.now()}
	if errMsg != "" {
		updates["error"] = errMsg
	}
	ok, err := p.docs.TransitionTaskIfRunning(ctx, t.ID, updates)
	if err != nil {
		return err
	}
	if !ok {
		slog.Warn("任务已被先行终结（僵尸回收/级联删除），放弃收尾覆写",
			"task", t.ID, "intended_status", status)
	}
	return nil
}

// abandonFenced 任务已被僵尸回收/终结：放弃本次处理。
// 不返回错误（asynq 不重投——重投只会再次被 fencing 拒绝），也不覆写文档状态：
// 文档已由回收方（sweeper/重启恢复）置 failed，用户 Retry 后由新任务重新接管。
func (p *Pipeline) abandonFenced(t *model.IngestTask, d *model.Document) error {
	slog.Warn("任务已被先行终结（僵尸回收），放弃本次写入",
		"task", t.ID, "doc", d.ID, "worker_id", p.workerID)
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
