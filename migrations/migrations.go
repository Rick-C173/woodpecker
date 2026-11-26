// Package migrations 嵌入版本化迁移脚本，供 internal/database 启动时执行。
// 迁移只增不改（开发计划 §4）：000002 起为增量演进，禁止合并回 000001——
// 合并会破坏已部署库的 golang-migrate 版本记录（dirty/版本对不上导致启动失败）。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
