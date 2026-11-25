package probe_test

// P0-2 / P0-3 回归探针（审计 2026-11-24，原为 /tmp 下的缺陷复现用例，修复后固化为回归测试）：
//  1) 真库 + 已取消 ctx 时 CreateMessage 是否立即失败 —— 证明「SSE 中断不落库」的机制根因，
//     也是 service/chat.go 必须用 context.WithoutCancel 落库的依据。
//  2) 「最后一个管理员」保护能否被「先禁用其他管理员」绕过 —— 系统可用管理员不得降到 0。
//
// 断言写的是**正确行为**：修复前 1) 复现失败机制成立、2) 撤销返回 nil 且可用管理员 0。

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"woodpecker/internal/database"
	"woodpecker/internal/model"
	"woodpecker/internal/repo"
	"woodpecker/internal/service"
	"woodpecker/internal/testutil"
)

func strPtr(s string) *string { return &s }

// probeDSN 测试库 DSN（可用 TEST_DSN 覆盖）。
func probeDSN() string {
	if dsn := os.Getenv("TEST_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://woodpecker:woodpecker@127.0.0.1:5432/woodpecker_test?sslmode=disable"
}

// openDB 连接测试库；PG 不可达时跳过（不硬编码、不 os.Exit）。
// 用例会 TRUNCATE 业务表，故先取跨包互斥锁，避免与 internal/worker 的探针互相清数据。
func openDB(t *testing.T) *gorm.DB {
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
	require.NoError(t, database.Migrate(dsn))
	require.NoError(t, db.Exec(`TRUNCATE users, audit_logs, settings, model_services, model_entries,
		knowledge_bases, kb_members, documents, document_files, ingest_tasks, chunks,
		conversations, messages, message_citations, feedbacks RESTART IDENTITY CASCADE`).Error)
	return db
}

// TestProbeCancelledCtxBlocksSave 证明：真库 + 已取消 ctx → 写入立即失败。
// 即 service/chat.go 若沿用入参 ctx 落库，客户端断连后部分回答必然丢失（P0-2 的机制根因）；
// 修复后在 chat.go 落库处改用 context.WithoutCancel，本用例继续守护该机制不被「优化」掉。
func TestProbeCancelledCtxBlocksSave(t *testing.T) {
	db := openDB(t)
	r := repo.NewConversationRepo(db)

	u := &model.User{Username: "p1", PasswordHash: "x", Status: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, db.Create(u).Error)
	kb := &model.KnowledgeBase{Name: "kb", CreatedBy: u.ID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, db.Create(kb).Error)
	c := &model.Conversation{KBID: kb.ID, UserID: u.ID, Title: strPtr("t"), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, r.CreateConversation(context.Background(), c))

	// 场景：客户端断开 → ctx 已取消 → service 若仍用该 ctx 写库则必然失败
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 模拟客户端已断开

	err := r.CreateMessage(ctx, &model.Message{
		ConversationID: c.ID, Role: model.MsgRoleAssistant, Content: "部分回答", Interrupted: true, CreatedAt: time.Now(),
	})
	t.Logf("【已取消 ctx 下 CreateMessage 的返回】err = %v", err)
	require.Error(t, err, "预期真库拒绝已取消 ctx 的写入")
	if !errors.Is(err, context.Canceled) {
		t.Logf("（错误类型不是 context.Canceled，而是 %T: %v）", err, err)
	}

	// 确认确实没落库
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM messages WHERE conversation_id = ?`, c.ID).Scan(&n).Error)
	t.Logf("【messages 表实际条数】%d", n)
	require.Equal(t, int64(0), n, "已取消 ctx 下不应有任何 message 落库")

	// 对照：用新 ctx 可以正常写入 —— 证明失败原因确为 ctx 取消，而非数据问题
	require.NoError(t, r.CreateMessage(context.Background(), &model.Message{
		ConversationID: c.ID, Role: model.MsgRoleAssistant, Content: "部分回答", Interrupted: true, CreatedAt: time.Now(),
	}), "同一写入换成未取消的 ctx 应成功")
	t.Logf("✅ 对照：换成未取消 ctx 后写入成功 —— 说明失败原因确为 ctx 取消，而非数据问题")
}

// TestProbeLastAdminBypass A、B 均为管理员 → 禁用 B → 撤销 A 的管理员必须返回 USER_003，
// 且系统可用管理员不得降到 0（防后台永久锁死）。
func TestProbeLastAdminBypass(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	users := repo.NewUserRepo(db)
	auditSvc := service.NewAuditService(repo.NewAuditRepo(db))
	svc := service.NewUserAdminService(users, auditSvc)

	// A、B 都是管理员且都启用
	a := &model.User{Username: "adminA", PasswordHash: "x", IsAdmin: true, Status: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	b := &model.User{Username: "adminB", PasswordHash: "x", IsAdmin: true, Status: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, db.Create(a).Error)
	require.NoError(t, db.Create(b).Error)

	// 1) 禁用 B（B 变成"不可用"，但 is_admin 仍为 true，status=2）
	require.NoError(t, svc.SetStatus(ctx, a.ID, b.ID, true, "127.0.0.1"), "禁用 B")
	var bAfter model.User
	require.NoError(t, db.First(&bAfter, b.ID).Error)
	t.Logf("【禁用后 B】status=%d is_admin=%v", bAfter.Status, bAfter.IsAdmin)
	require.Equal(t, model.UserStatusDisabled, bAfter.Status)
	require.True(t, bAfter.IsAdmin, "禁用保留 is_admin=true，故计数必须过滤 status")

	// 2) 撤销 A 的管理员（此时唯一"可用"的管理员就是 A）
	err := svc.SetAdmin(ctx, a.ID, a.ID, false, "127.0.0.1")
	t.Logf("【撤销 A 的管理员权限】err = %v", err)

	// 3) 统计"可用管理员"数量
	var usable int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM users WHERE is_admin = true AND status = 1`).Scan(&usable).Error)
	var allAdmins int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM users WHERE is_admin = true`).Scan(&allAdmins).Error)
	t.Logf("【实际情况】is_admin=true 共 %d 人；其中 status=1（可用）%d 人", allAdmins, usable)

	require.NotZero(t, usable, "撤销后系统不得变成 0 个可用管理员（后台永久锁死）")
	require.Error(t, err, "撤销唯一可用管理员必须被拦截")

	var se *service.ServiceError
	require.ErrorAs(t, err, &se, "应返回业务错误 USER_003")
	require.Equal(t, service.CodeUserLastAdmin, se.Code, "应返回 USER_003（最后一个管理员不可被降级）")
	t.Logf("✅ 被拦截：code=%v message=%s", se.Code, se.Message)
}
