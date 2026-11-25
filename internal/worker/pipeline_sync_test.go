package worker_test

// P0-1 回归探针（审计 2026-11-24，原为 /tmp 下的缺陷复现用例，修复后固化为回归测试）：
// 验证「增量同步（sync）不会清空未变更文件的分块」。
//
// 断言写的是**正确行为**：sync 只替换变更文件的分块，未变更文件的分块原样保留。
// 修复前全红（chunks 5 → 0，状态仍为 ready 的静默数据丢失）。

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
	"woodpecker/internal/testutil"
	"woodpecker/internal/worker"
)

// probeEmbedder 返回 1024 维向量（chunks.embedding 列要求 1024 维）。
type probeEmbedder struct{ calls int }

func (p *probeEmbedder) Embed(_ context.Context, _ llm.ResolvedConfig, texts []string) ([][]float32, error) {
	p.calls++
	out := make([][]float32, len(texts))
	for i := range out {
		v := make([]float32, 1024)
		for j := range v {
			v[j] = 0.01
		}
		out[i] = v
	}
	return out, nil
}

// probeDSN 测试库 DSN（可用 TEST_DSN 覆盖）。
func probeDSN() string {
	if dsn := os.Getenv("TEST_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker_test?sslmode=disable"
}

// probeOpenDB 连接测试库；PG 不可达时跳过（不硬编码、不 os.Exit）。
// 用例会 TRUNCATE 业务表，故先取跨包互斥锁，避免与 internal/probe 的探针互相清数据。
func probeOpenDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := probeDSN()
	db, err := database.Open(dsn, 20, 5, 30)
	if err != nil {
		t.Skipf("P0 回归探针需要 PostgreSQL（设置 TEST_DSN）: %v", err)
	}
	sqlDB, derr := db.DB()
	if derr != nil {
		t.Skipf("P0 回归探针需要 PostgreSQL（设置 TEST_DSN）: %v", derr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if perr := sqlDB.PingContext(ctx); perr != nil {
		t.Skipf("P0 回归探针需要 PostgreSQL（设置 TEST_DSN）: %v", perr)
	}
	testutil.AcquireDBProbeLock(t, db)
	require.NoError(t, database.Migrate(dsn), "迁移")
	require.NoError(t, db.Exec(`TRUNCATE users, audit_logs, settings, model_services, model_entries,
		knowledge_bases, kb_members, documents, document_files, ingest_tasks, chunks,
		conversations, messages, message_citations, feedbacks RESTART IDENTITY CASCADE`).Error)
	return db
}

// probeSeed 建一个用户 + 一个知识库，返回 userID、kbID。
func probeSeed(t *testing.T, db *gorm.DB, username string) (userID, kbID int64) {
	t.Helper()
	u := &model.User{Username: username, PasswordHash: "x", IsAdmin: true, Status: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, db.Create(u).Error, "建用户")
	kb := &model.KnowledgeBase{Name: "probe-kb", CreatedBy: u.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, db.Create(kb).Error, "建知识库")
	return u.ID, kb.ID
}

func probeChunkCount(t *testing.T, db *gorm.DB, docID int64) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM chunks WHERE document_id = ?`, docID).Scan(&n).Error)
	return n
}

// probeChunkCountsByFile 按文件统计分块数（验收回归 8-2 断言用）。
func probeChunkCountsByFile(t *testing.T, db *gorm.DB, docID int64) map[string]int64 {
	t.Helper()
	type row struct {
		FilePath string
		N        int64
	}
	var rows []row
	require.NoError(t, db.Raw(
		`SELECT file_path, count(*) AS n FROM chunks WHERE document_id = ? GROUP BY file_path`, docID).
		Scan(&rows).Error)
	out := map[string]int64{}
	for _, r := range rows {
		out[r.FilePath] = r.N
	}
	return out
}

// TestProbeSyncWipesChunks 全量摄取 → 增量同步（文件内容未变）→ 分块必须原样保留。
func TestProbeSyncWipesChunks(t *testing.T) {
	db := probeOpenDB(t)
	ctx := context.Background()
	userID, kbID := probeSeed(t, db, "probe")

	uploadDir := t.TempDir()
	docs := repo.NewDocumentRepo(db)
	p := worker.NewPipeline(docs, nil, &testutil.FakeCipher{}, &fakeResolver{}, &probeEmbedder{}, &fakeQueue{})
	p.SetUploadDir(uploadDir)

	d := &model.Document{KBID: kbID, Type: model.DocTypeUpload, Name: "probe", Status: model.DocStatusPending, CreatedBy: userID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, docs.CreateDocument(ctx, d))

	dir := filepath.Join(uploadDir, fmt.Sprint(d.KBID), fmt.Sprint(d.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# 指南\n\n这是第一段内容。\n\n## 小节\n\n第二段内容。\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\n// A 函数\nfunc A() {}\n\n// B 函数\nfunc B() {}\n"), 0o600))

	// ---- 第一次：全量摄取 ----
	full := &model.IngestTask{DocumentID: d.ID, Type: model.TaskTypeFull, Status: model.TaskStatusQueued, CreatedAt: time.Now()}
	require.NoError(t, docs.CreateTask(ctx, full))
	require.NoError(t, p.Process(ctx, full.ID), "全量摄取应成功")

	afterFull := probeChunkCount(t, db, d.ID)
	t.Logf("【全量摄取后】分块数 = %d", afterFull)
	require.Positive(t, afterFull, "全量摄取后应产生分块")

	// ---- 第二次：增量同步，文件内容一个字节都没改 ----
	syncTask := &model.IngestTask{DocumentID: d.ID, Type: model.TaskTypeSync, Status: model.TaskStatusQueued, CreatedAt: time.Now()}
	require.NoError(t, docs.CreateTask(ctx, syncTask))
	require.NoError(t, p.Process(ctx, syncTask.ID), "增量同步应成功")

	afterSync := probeChunkCount(t, db, d.ID)
	t.Logf("【增量同步后】分块数 = %d", afterSync)

	var status int16
	require.NoError(t, db.Raw(`SELECT status FROM documents WHERE id = ?`, d.ID).Scan(&status).Error)
	t.Logf("【同步后文档状态】status = %d（DocStatusReady=%d）", status, model.DocStatusReady)

	require.Equal(t, afterFull, afterSync, "无变更的增量同步不得改动任何分块")
	require.NotZero(t, afterSync, "无变更的增量同步后分块不得归零")
	require.Equal(t, model.DocStatusReady, status, "同步后文档应为已就绪")
}

// TestProbeSyncPartialChange 只改 a.md，b.md 的分块必须原样保留。
func TestProbeSyncPartialChange(t *testing.T) {
	db := probeOpenDB(t)
	ctx := context.Background()
	userID, kbID := probeSeed(t, db, "probe2")

	uploadDir := t.TempDir()
	docs := repo.NewDocumentRepo(db)
	p := worker.NewPipeline(docs, nil, &testutil.FakeCipher{}, &fakeResolver{}, &probeEmbedder{}, &fakeQueue{})
	p.SetUploadDir(uploadDir)

	d := &model.Document{KBID: kbID, Type: model.DocTypeUpload, Name: "probe2", Status: model.DocStatusPending, CreatedBy: userID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, docs.CreateDocument(ctx, d))
	dir := filepath.Join(uploadDir, fmt.Sprint(d.KBID), fmt.Sprint(d.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\nA 文件内容。\n\n## A2\n\n更多 A 内容。\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("# B\n\nB 文件内容。\n\n## B2\n\n更多 B 内容。\n"), 0o600))

	full := &model.IngestTask{DocumentID: d.ID, Type: model.TaskTypeFull, Status: model.TaskStatusQueued, CreatedAt: time.Now()}
	require.NoError(t, docs.CreateTask(ctx, full))
	require.NoError(t, p.Process(ctx, full.ID))

	type chunkRow struct {
		FilePath string
		N        int64
	}
	var before []chunkRow
	require.NoError(t, db.Raw(`SELECT file_path, count(*) AS n FROM chunks WHERE document_id = ? GROUP BY file_path ORDER BY file_path`, d.ID).Scan(&before).Error)
	t.Logf("【全量后按文件分块】%+v", before)
	require.Len(t, before, 2, "全量后应有 a.md / b.md 两个文件的分块")

	// 只改 a.md
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\nA 文件内容（已修改）。\n\n## A2\n\n更多 A 内容。\n"), 0o600))

	syncTask := &model.IngestTask{DocumentID: d.ID, Type: model.TaskTypeSync, Status: model.TaskStatusQueued, CreatedAt: time.Now()}
	require.NoError(t, docs.CreateTask(ctx, syncTask))
	require.NoError(t, p.Process(ctx, syncTask.ID))

	var after []chunkRow
	require.NoError(t, db.Raw(`SELECT file_path, count(*) AS n FROM chunks WHERE document_id = ? GROUP BY file_path ORDER BY file_path`, d.ID).Scan(&after).Error)
	t.Logf("【仅改 a.md 后按文件分块】%+v", after)

	byFile := map[string]int64{}
	for _, r := range after {
		byFile[r.FilePath] = r.N
	}
	for _, r := range before {
		if r.FilePath == "a.md" {
			continue // 变更文件会被重切，数量可变化
		}
		require.Equal(t, r.N, byFile[r.FilePath], "未变更文件 %s 的分块数必须不变", r.FilePath)
		require.NotZero(t, byFile[r.FilePath], "未变更文件 %s 的分块不得被清空", r.FilePath)
	}
	require.NotZero(t, byFile["a.md"], "变更文件 a.md 应有分块")
}

// TestProbeSyncClearsChunksWhenFileBecomesEmpty 验收回归 8-2：某文件被改成「切不出块」的内容
// （典型：用户把文件清空）后，增量同步必须删掉该文件的旧分块，且不影响其它文件。
//
// 修复前全红：buildChunks 已更新该文件的 content_hash（后续 sync 会跳过它），
// 而 embedAndStore 见 len(flat)==0 就当「本轮无变更文件」早退，DeleteChunksByFile 从未执行
// → 已删除的内容永久可被检索命中。
func TestProbeSyncClearsChunksWhenFileBecomesEmpty(t *testing.T) {
	db := probeOpenDB(t)
	ctx := context.Background()
	userID, kbID := probeSeed(t, db, "probe-empty")

	uploadDir := t.TempDir()
	docs := repo.NewDocumentRepo(db)
	p := worker.NewPipeline(docs, nil, &testutil.FakeCipher{}, &fakeResolver{}, &probeEmbedder{}, &fakeQueue{})
	p.SetUploadDir(uploadDir)

	d := &model.Document{KBID: kbID, Type: model.DocTypeUpload, Name: "probe-empty", Status: model.DocStatusPending, CreatedBy: userID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, docs.CreateDocument(ctx, d))
	dir := filepath.Join(uploadDir, fmt.Sprint(d.KBID), fmt.Sprint(d.ID))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\n内容甲。\n\n## A2\n\n甲二。\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("# B\n\n内容乙。\n\n## B2\n\n乙二。\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.md"), []byte("# C\n\n内容丙。\n"), 0o600))

	full := &model.IngestTask{DocumentID: d.ID, Type: model.TaskTypeFull, Status: model.TaskStatusQueued, CreatedAt: time.Now()}
	require.NoError(t, docs.CreateTask(ctx, full))
	require.NoError(t, p.Process(ctx, full.ID))

	before := probeChunkCountsByFile(t, db, d.ID)
	t.Logf("【全量后按文件分块】%+v", before)
	require.Positive(t, before["a.md"], "a.md 应有分块")
	require.Positive(t, before["b.md"])
	require.Positive(t, before["c.md"])

	// 把 a.md 清空（用户删除全部内容）→ 该文件切不出任何块
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte(""), 0o600))

	syncTask := &model.IngestTask{DocumentID: d.ID, Type: model.TaskTypeSync, Status: model.TaskStatusQueued, CreatedAt: time.Now()}
	require.NoError(t, docs.CreateTask(ctx, syncTask))
	require.NoError(t, p.Process(ctx, syncTask.ID))

	after := probeChunkCountsByFile(t, db, d.ID)
	t.Logf("【a.md 清空后按文件分块】before=%v after=%v", before, after)

	var status int16
	require.NoError(t, db.Raw(`SELECT status FROM documents WHERE id = ?`, d.ID).Scan(&status).Error)
	require.Equal(t, model.DocStatusReady, status, "同步后文档应仍为已就绪")

	assert.Zero(t, after["a.md"], "a.md 已清空 → 旧分块必须被删除（否则旧内容永久可检索）")
	assert.Equal(t, before["b.md"], after["b.md"], "b.md 未变更 → 分块数必须不变")
	assert.Equal(t, before["c.md"], after["c.md"], "c.md 未变更 → 分块数必须不变")
}
