// Package migrations 嵌入版本化迁移脚本，供 internal/database 启动时执行。
//
// 迁移纪律（开发计划 §4）：
//   - 000001_schema 是 v1 合并基线（2026-11-26 squash，终态 schema）；
//   - 其后每张新表/新列一律新增 00000N_*.sql 增量文件，禁止改写已有文件——
//     已部署库的 schema_migrations 记录版本号与校验和，改写会导致 dirty 拒绝启动或列缺失；
//   - 再次 squash 的唯一条件：所有部署环境均可 DROP 重建（无生产数据窗口期），人工决策执行，
//     届时把全部增量合并回基线文件。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
