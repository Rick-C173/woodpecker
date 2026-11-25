//go:build integration

// Package qa_probe 是 QA 的**独立** P0 回归验收（不复用仓库既有探针）。
// 运行：TEST_DSN=... go test -tags=integration -p 1 -count=1 -run TestQA -v ./internal/qa_probe/
//
// 与既有 probe 的区别：场景由 QA 自行构造（3 文件真实上传目录 + 真实向量化替身 + 真库），
// 断言到「每文件分块数」这一级，可直接被第三方复现。
package qa_probe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"woodpecker/internal/database"
	"woodpecker/internal/llm"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
	"woodpecker/internal/worker"
)

func openDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		dsn = "postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker_test?sslmode=disable"
	}
	db, err := database.Open(dsn, 10, 5, 30)
	require.NoError(t, err)
	require.NoError(t, database.Migrate(dsn))
	return db
}

func truncate(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`TRUNCATE users, audit_logs, settings, model_services, model_entries,
		knowledge_bases, kb_members, documents, document_files, ingest_tasks, chunks,
		conversations, messages, message_citations, feedbacks RESTART IDENTITY CASCADE`).Error)
}

func seedUserKB(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO users (id, username, password_hash) OVERRIDING SYSTEM VALUE VALUES (1, 'qa', 'x')`).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, name, created_by) OVERRIDING SYSTEM VALUE VALUES (1, 'qa-kb', 1)`).Error)
}

// ---- 替身：向量化与默认模型配置（与向量维度无关，只验证管道行为） ----

// noopQueue 入队替身（本探针不关心队列）。
type noopQueue struct{}

func (noopQueue) EnqueueIngest(context.Context, int64) error { return nil }

type fakeEmbedder struct{ calls int }

func (f *fakeEmbedder) Embed(_ context.Context, _ llm.ResolvedConfig, texts []string) ([][]float32, error) {
	f.calls++
	out := make([][]float32, len(texts))
	for i := range texts {
		v := make([]float32, 1024)
		v[0] = float32(i+1) / 100
		out[i] = v
	}
	return out, nil
}

type fakeResolver struct{}

func (fakeResolver) GetDefault(_ context.Context, _ int16) (llm.ResolvedConfig, error) {
	return llm.ResolvedConfig{ModelName: "fake-embed", BaseURL: "http://x", APIKey: "k"}, nil
}

func chunkCounts(t *testing.T, db *gorm.DB, docID int64) map[string]int64 {
	t.Helper()
	type row struct {
		FilePath *string
		N        int64
	}
	var rows []row
	require.NoError(t, db.Raw(
		`SELECT file_path, COUNT(*) AS n FROM chunks WHERE document_id = ? GROUP BY file_path`, docID).
		Scan(&rows).Error)
	out := map[string]int64{}
	for _, r := range rows {
		key := "<nil>"
		if r.FilePath != nil {
			key = *r.FilePath
		}
		out[key] = r.N
	}
	return out
}

func docStatus(t *testing.T, db *gorm.DB, docID int64) (int16, *string) {
	t.Helper()
	var d struct {
		Status int16
		Stage  *int16
		Error  *string
	}
	require.NoError(t, db.Raw(`SELECT status, stage, error FROM documents WHERE id = ?`, docID).Scan(&d).Error)
	return d.Status, d.Error
}

// makeUploadDoc 造「上传文档 + 3 个文件已落地」，返回 docID。
func makeUploadDoc(t *testing.T, _ *gorm.DB, docs *repo.DocumentRepo, uploadRoot string, files map[string]string) (docID, taskID int64) {
	t.Helper()
	ctx := context.Background()
	d := &model.Document{KBID: 1, Type: model.DocTypeUpload, Name: "qa-doc", CreatedBy: 1, Status: model.DocStatusPending}
	task := &model.IngestTask{Type: model.TaskTypeFull, Status: model.TaskStatusQueued}
	require.NoError(t, docs.CreateDocumentWithTask(ctx, d, task))

	dir := filepath.Join(uploadRoot, "1", fmt.Sprint(d.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}
	// CreateDocumentWithTask 已建好排队任务（status=1），直接复用；再插一条会撞 uq_ingest_tasks_running
	return d.ID, task.ID
}

func newPipeline(docs *repo.DocumentRepo, settings *repo.SettingRepo, uploadRoot string) *worker.Pipeline {
	p := worker.NewPipeline(docs, settings, nil, fakeResolver{}, &fakeEmbedder{}, nil)
	p.SetUploadDir(uploadRoot)
	p.SetWorkerID("qa-independent")
	return p
}

func syncTask(t *testing.T, db *gorm.DB, docID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, db.Raw(
		`INSERT INTO ingest_tasks (document_id, type, status) VALUES (?, 2, 1) RETURNING id`, docID).
		Scan(&id).Error)
	return id
}

// ---------------------------------------------------------------------------
// P0-1 独立验证：增量同步按文件替换，未变更文件的分块必须原样保留
// ---------------------------------------------------------------------------
func TestQA_P0_1_IncrementalSyncKeepsUnchangedFileChunks(t *testing.T) {
	db := openDB(t)
	truncate(t, db)
	seedUserKB(t, db)
	docs := repo.NewDocumentRepo(db)
	settings := repo.NewSettingRepo(db)
	uploadRoot := t.TempDir()

	// 三个文件，每个内容量不同 → 分块数不同，能区分「哪个文件被动了」
	files := map[string]string{
		"a.md": "# A\n\n内容甲。\n\n## A2\n\n甲二。\n",
		"b.md": "# B\n\n内容乙。\n\n## B2\n\n乙二。\n\n## B3\n\n乙三。\n",
		"c.md": "# C\n\n内容丙。\n",
	}
	docID, firstTaskID := makeUploadDoc(t, db, docs, uploadRoot, files)
	p := newPipeline(docs, settings, uploadRoot)
	ctx := context.Background()

	// --- 全量摄取 ---
	require.NoError(t, p.Process(ctx, firstTaskID))
	st, errMsg := docStatus(t, db, docID)
	require.Equal(t, model.DocStatusReady, st, "全量摄取后应 ready, error=%v", errMsg)

	before := chunkCounts(t, db, docID)
	t.Logf("全量摄取后每文件分块数: %v", before)
	require.Len(t, before, 3, "三个文件都应有分块")
	require.Positive(t, before["a.md"])
	require.Positive(t, before["b.md"])
	require.Positive(t, before["c.md"])

	// --- 只改 1 个文件（a.md），执行增量同步 ---
	newA := "# A\n\n内容甲（已修改）。\n\n## A2\n\n甲二改。\n\n## A3\n\n新增甲三。\n"
	require.NoError(t, os.WriteFile(filepath.Join(uploadRoot, "1", fmt.Sprint(docID), "a.md"), []byte(newA), 0o600))
	require.NoError(t, p.Process(ctx, syncTask(t, db, docID)))

	afterSync := chunkCounts(t, db, docID)
	t.Logf("仅改 a.md 同步后每文件分块数: %v", afterSync)

	st2, errMsg2 := docStatus(t, db, docID)
	assert.Equal(t, model.DocStatusReady, st2, "增量同步后文档应仍 ready, error=%v", errMsg2)
	assert.Equal(t, before["b.md"], afterSync["b.md"], "b.md 未变更 → 分块数必须不变（P0-1 核心）")
	assert.Equal(t, before["c.md"], afterSync["c.md"], "c.md 未变更 → 分块数必须不变（P0-1 核心）")
	assert.NotEqual(t, before["a.md"], afterSync["a.md"], "a.md 已变更 → 分块应被替换")
	assert.Positive(t, afterSync["a.md"])

	// 未变更文件的块内容也必须原样（不只是数量）
	var bContent string
	require.NoError(t, db.Raw(
		`SELECT content FROM chunks WHERE document_id = ? AND file_path = 'b.md' ORDER BY chunk_index LIMIT 1`, docID).
		Scan(&bContent).Error)
	assert.Contains(t, bContent, "内容乙", "未变更文件的分块内容必须原样保留，不能被清空重建")

	// --- 再同步一次：全未变更，分块数必须完全不变 ---
	require.NoError(t, p.Process(ctx, syncTask(t, db, docID)))
	afterNoChange := chunkCounts(t, db, docID)
	t.Logf("全未变更同步后每文件分块数: %v", afterNoChange)
	assert.Equal(t, afterSync, afterNoChange, "全未变更的同步不得改变任何分块（旧实现会整篇清空）")

	st3, _ := docStatus(t, db, docID)
	assert.Equal(t, model.DocStatusReady, st3)
}

// ---------------------------------------------------------------------------
// P0-1 附加回归：文件被改成「切不出块」的内容后，旧分块必须被清理
// （QA 独立发现于验收，验收回归 8-2；修复后转为回归用例）
// ---------------------------------------------------------------------------
func TestQA_P0_1_ChangedFileToEmptyClearsChunks(t *testing.T) {
	db := openDB(t)
	truncate(t, db)
	seedUserKB(t, db)
	docs := repo.NewDocumentRepo(db)
	settings := repo.NewSettingRepo(db)
	uploadRoot := t.TempDir()

	files := map[string]string{
		"a.md": "# A\n\n内容甲。\n\n## A2\n\n甲二。\n",
		"b.md": "# B\n\n内容乙。\n",
	}
	docID, firstTaskID := makeUploadDoc(t, db, docs, uploadRoot, files)
	p := newPipeline(docs, settings, uploadRoot)
	ctx := context.Background()

	require.NoError(t, p.Process(ctx, firstTaskID))
	before := chunkCounts(t, db, docID)
	require.Positive(t, before["a.md"])
	require.Positive(t, before["b.md"])

	// 把 a.md 改成空文件（用户清空内容）→ 该文件切不出任何块
	require.NoError(t, os.WriteFile(filepath.Join(uploadRoot, "1", fmt.Sprint(docID), "a.md"), []byte(""), 0o600))
	require.NoError(t, p.Process(ctx, syncTask(t, db, docID)))

	after := chunkCounts(t, db, docID)
	t.Logf("a.md 清空后分块数: before=%v after=%v", before, after)

	assert.Zero(t, after["a.md"], "a.md 已清空 → 旧分块必须被删除，不得残留可被检索的旧内容")
	assert.Equal(t, before["b.md"], after["b.md"], "b.md 未变更 → 分块数必须不变")

	st, errMsg := docStatus(t, db, docID)
	assert.Equal(t, model.DocStatusReady, st, "同步后文档应仍 ready, error=%v", errMsg)
}

// ---------------------------------------------------------------------------
// P0-2 独立验证：客户端断连（ctx 取消）后，已生成部分必须落库且 interrupted=true
// ---------------------------------------------------------------------------

// fakeStreamer 忽略 ctx 持续吐 delta（模拟「模型侧还在推，客户端已断」）。
type fakeStreamer struct{ n int }

func (f *fakeStreamer) ChatStream(context.Context, llm.ResolvedConfig, llm.ChatRequest) (<-chan llm.ChatDelta, error) {
	ch := make(chan llm.ChatDelta, f.n)
	for i := 0; i < f.n; i++ {
		ch <- llm.ChatDelta{Content: fmt.Sprintf("片段%d ", i)}
	}
	close(ch)
	return ch, nil
}

type fakeChatResolver struct{}

func (fakeChatResolver) GetDefault(_ context.Context, _ int16) (llm.ResolvedConfig, error) {
	return llm.ResolvedConfig{EntryID: 0, ModelName: "fake-chat"}, nil
}

type fakeSearch struct{}

func (fakeSearch) SearchTopN(context.Context, int64, string, int) ([]service.SearchItem, error) {
	return nil, nil
}

func seedConversation(t *testing.T, db *gorm.DB) (convID int64) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO conversations (id, kb_id, user_id, title) OVERRIDING SYSTEM VALUE VALUES (1, 1, 1, NULL)`).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO kb_members (kb_id, user_id, role) VALUES (1, 1, 1)`).Error)
	return 1
}

func waitAssistant(t *testing.T, db *gorm.DB, convID int64, timeout time.Duration) (content string, interrupted bool, ok bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var row struct {
			Content     string
			Interrupted bool
		}
		err := db.Raw(`SELECT content, interrupted FROM messages
			WHERE conversation_id = ? AND role = 2 ORDER BY id DESC LIMIT 1`, convID).Scan(&row).Error
		if err == nil && row.Content != "" {
			return row.Content, row.Interrupted, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", false, false
}

func TestQA_P0_2_CancelledContextStillPersistsInterrupted(t *testing.T) {
	db := openDB(t)
	truncate(t, db)
	seedUserKB(t, db)
	convID := seedConversation(t, db)

	convs := repo.NewConversationRepo(db)
	kbs := repo.NewKBRepo(db)
	streamer := &fakeStreamer{n: 200} // 足够多，确保 ctx 取消后必然中断
	svc := service.NewChatService(convs, kbs, fakeChatResolver{}, streamer, fakeSearch{})

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := svc.Ask(ctx, convID, 1, false, "请介绍一下限流")
	require.NoError(t, err)
	// 模拟客户端断开：立刻取消请求 ctx，且**不再读取**事件通道（真实断连场景）
	cancel()

	content, interrupted, ok := waitAssistant(t, db, convID, 5*time.Second)
	require.True(t, ok, "断连后 assistant 消息必须落库（P0-2 核心）")
	t.Logf("断连落库: 长度=%d interrupted=%v", len([]rune(content)), interrupted)
	assert.True(t, interrupted, "断连落库的消息必须标记 interrupted=true")
	assert.NotEmpty(t, content, "已生成的部分必须保留，不能是空字符串")

	// 用户消息（提问）同样保留
	var userCount int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM messages WHERE conversation_id = ? AND role = 1`, convID).
		Scan(&userCount).Error)
	assert.Equal(t, int64(1), userCount)

	// 收尾，避免 goroutine 泄漏影响后续用例
	for range ch { //nolint:revive // 排空至关闭
	}
}

// 对照组：正常完成后不受影响（interrupted=false，内容完整）
func TestQA_P0_2_ControlNormalCompletion(t *testing.T) {
	db := openDB(t)
	truncate(t, db)
	seedUserKB(t, db)
	convID := seedConversation(t, db)

	convs := repo.NewConversationRepo(db)
	kbs := repo.NewKBRepo(db)
	svc := service.NewChatService(convs, kbs, fakeChatResolver{}, &fakeStreamer{n: 3}, fakeSearch{})

	ch, err := svc.Ask(context.Background(), convID, 1, false, "正常提问")
	require.NoError(t, err)
	var got int
	for range ch {
		got++
	}
	require.Positive(t, got, "应收到 delta/done 事件")

	var row struct {
		Content     string
		Interrupted bool
	}
	require.NoError(t, db.Raw(`SELECT content, interrupted FROM messages
		WHERE conversation_id = ? AND role = 2 ORDER BY id DESC LIMIT 1`, convID).Scan(&row).Error)
	t.Logf("正常完成: 内容=%q interrupted=%v", row.Content, row.Interrupted)
	assert.False(t, row.Interrupted, "正常完成不得标记 interrupted")
	assert.Contains(t, row.Content, "片段0")
	assert.Contains(t, row.Content, "片段2", "正常完成应包含全部 delta")
}

// ---------------------------------------------------------------------------
// P0-3 独立验证：可用管理员保护（禁用管理员不计入可用管理员）
// ---------------------------------------------------------------------------
func TestQA_P0_3_LastAvailableAdminProtection(t *testing.T) {
	db := openDB(t)
	truncate(t, db)
	// A=启用管理员，B=禁用管理员
	require.NoError(t, db.Exec(`INSERT INTO users (id, username, password_hash, is_admin, status)
		OVERRIDING SYSTEM VALUE VALUES (1,'adminA','x',TRUE,1), (2,'adminB','x',TRUE,2), (3,'plain','x',FALSE,1)`).Error)

	users := repo.NewUserRepo(db)
	audit := service.NewAuditService(repo.NewAuditRepo(db))
	svc := service.NewUserAdminService(users, audit)
	ctx := context.Background()

	activeAdmins := func() int64 {
		var n int64
		require.NoError(t, db.Raw(`SELECT COUNT(*) FROM users WHERE is_admin AND status = 1`).Scan(&n).Error)
		return n
	}
	require.Equal(t, int64(1), activeAdmins(), "初始可用管理员应为 1（B 已被禁用）")

	// 1) 撤销 A（唯一可用管理员）的 admin → USER_003，且计数不变
	err := svc.SetAdmin(ctx, 3, 1, false, "ip")
	require.Error(t, err)
	var se *service.ServiceError
	require.ErrorAs(t, err, &se, "应为 ServiceError，实际 %T", err)
	t.Logf("撤销唯一可用管理员返回: %s (%s)", se.Code, se.Message)
	assert.Equal(t, service.CodeUserLastAdmin, se.Code, "必须返回 USER_003")
	assert.Equal(t, int64(1), activeAdmins(), "被拦截后可用管理员数必须不变")

	// 2) 禁用最后一名可用管理员 → USER_002 拦截
	err = svc.SetStatus(ctx, 3, 1, true, "ip")
	require.Error(t, err)
	var se2 *service.ServiceError
	require.ErrorAs(t, err, &se2)
	t.Logf("禁用最后一名可用管理员返回: %s (%s)", se2.Code, se2.Message)
	assert.Equal(t, service.CodeUserLastAdmin, se2.Code, "必须返回 USER_002")

	// 3) 重复禁用「已禁用」的管理员 B → 幂等放行（不因最后一名保护误拦）
	require.NoError(t, svc.SetStatus(ctx, 3, 2, true, "ip"), "重复禁用已禁用用户应幂等放行")

	// 4) 新增一名可用管理员后，撤销 A 应放行
	require.NoError(t, svc.SetAdmin(ctx, 3, 3, true, "ip"))
	require.Equal(t, int64(2), activeAdmins())
	require.NoError(t, svc.SetAdmin(ctx, 3, 1, false, "ip"), "还有另一名可用管理员时应允许降级")
	assert.Equal(t, int64(1), activeAdmins())
}

// ---------------------------------------------------------------------------
// P1-8 跨重启回归：worker 标识跨重启变化（如 k8s Pod 重建 → 主机名变化）时，
// 上一实例遗留的 running 任务必须被「心跳时间兜底」回收，不能永久占住
// uq_ingest_tasks_running 挡住 retry/sync/reindex（验收回归 8-1，修复后转为回归用例）。
// ---------------------------------------------------------------------------
func TestQA_P1_8_WorkerIDChangedAcrossRestartReclaimsTask(t *testing.T) {
	db := openDB(t)
	truncate(t, db)
	seedUserKB(t, db)
	docs := repo.NewDocumentRepo(db)
	settings := repo.NewSettingRepo(db)
	uploadRoot := t.TempDir()
	ctx := context.Background()

	docID, _ := makeUploadDoc(t, db, docs, uploadRoot, map[string]string{"a.md": "# A\n\nx\n"})

	// 上一实例（pod-aaa）处理到一半就崩了：任务 running、归属 pod-aaa，最后一次心跳在 10 分钟前
	var staleTaskID int64
	require.NoError(t, db.Raw(`UPDATE ingest_tasks
		SET status = 2, worker_id = 'pod-aaa', started_at = now() - interval '10 minutes',
		    heartbeat_at = now() - interval '10 minutes'
		WHERE document_id = ? RETURNING id`, docID).Scan(&staleTaskID).Error)
	require.NoError(t, db.Exec(`UPDATE documents SET status = 2, stage = 1 WHERE id = ?`, docID).Error)

	// 新实例（pod-bbb）启动 → 重启恢复（默认阈值 5 分钟）
	p := newPipeline(docs, settings, uploadRoot)
	p.SetWorkerID("pod-bbb")
	p.RecoverStartup(ctx, docs)

	type trow struct {
		Status      int16
		WorkerID    *string
		HeartbeatAt *time.Time
	}
	var tr trow
	require.NoError(t, db.Raw(`SELECT status, worker_id, heartbeat_at FROM ingest_tasks WHERE id = ?`, staleTaskID).Scan(&tr).Error)
	st, errMsg := docStatus(t, db, docID)
	require.NotNil(t, tr.WorkerID)
	t.Logf("新实例恢复后：任务 status=%d worker_id=%s（任务 id=%d）；文档 status=%d error=%v",
		tr.Status, *tr.WorkerID, staleTaskID, st, errMsg)

	running, err := docs.HasRunningTask(ctx, docID)
	require.NoError(t, err)

	assert.Equal(t, model.TaskStatusFailed, tr.Status, "跨实例僵尸任务必须被置失败")
	assert.Equal(t, model.DocStatusFailed, st, "文档必须从处理中转为失败")
	assert.False(t, running, "不得再有运行中任务挡住 retry/reindex")

	// 用户侧恢复入口确实可用：失败态文档可以重试（旧实现这里返回 VALIDATION_FAILED）
	svc := service.NewDocumentService(docs, settings, nil, noopQueue{}, service.NewAuditService(repo.NewAuditRepo(db)))
	svc.SetUploadDir(uploadRoot)
	require.NoError(t, svc.Retry(ctx, 1, 1, docID, "ip"), "僵尸任务回收后 retry 必须可用")
}
