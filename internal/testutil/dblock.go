package testutil

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

// dbProbeLockKey 探针互斥键（任意常量，同一 PG 集群内唯一即可）。
const dbProbeLockKey int64 = 0x777070726f626501 // "wpprobe" + 0x01

// AcquireDBProbeLock 在共享测试库上取得会话级 advisory lock，测试结束自动释放。
//
// 用途：internal/worker 与 internal/probe 的 P0 回归探针都会 TRUNCATE 业务表（它们必须在
// 干净数据上断言「可用管理员 = 0」这类全局计数），而 `go test ./...` 会并行启动不同包的
// 测试二进制——两个包的 TRUNCATE 会互相清掉对方正在断言的数据。加锁把这类破坏性探针串行化。
func AcquireDBProbeLock(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("获取底层 sql.DB: %v", err)
	}
	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx) // 会话级锁必须在同一连接上加/解锁，故全程持有该连接
	if err != nil {
		t.Fatalf("占用数据库连接: %v", err)
	}
	var ignored any
	if err := conn.QueryRowContext(ctx, "SELECT pg_advisory_lock($1)", dbProbeLockKey).Scan(&ignored); err != nil {
		_ = conn.Close()
		t.Fatalf("获取测试库排它锁: %v", err)
	}
	t.Cleanup(func() {
		var unlocked any
		_ = conn.QueryRowContext(context.Background(),
			"SELECT pg_advisory_unlock($1)", dbProbeLockKey).Scan(&unlocked)
		_ = conn.Close()
	})
}
