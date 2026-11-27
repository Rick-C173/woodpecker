// Package migrations 嵌入 SQL 迁移脚本，供 internal/database 启动时按文件名字典序执行。
//
// 命名与合并纪律（开发阶段策略）：
//   - 基线 schema.up.sql：终态全量 DDL；
//   - 功能开发期可临时新增 schema_patch_<feature>.up.sql 增量文件——"." 字典序小于 "_"，
//     保证基线先于 patch 执行；多个 patch 有依赖时按依赖顺序命名 feature；
//   - 功能验证完毕：把 patch 内容合并回基线并删除 patch 文件，所有环境 DROP 重建；
//   - 一旦出现生产数据，该合并窗口永久关闭，届时转为只增不改。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
