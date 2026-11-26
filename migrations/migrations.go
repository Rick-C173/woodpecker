// Package migrations 嵌入版本化迁移脚本，供 internal/database 启动时执行。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

// TODO 合并初始数据库脚本 不要分成几个文件
