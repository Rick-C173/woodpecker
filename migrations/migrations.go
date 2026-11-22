// Package migrations 嵌入版本化迁移脚本，供 internal/database 启动时执行。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
