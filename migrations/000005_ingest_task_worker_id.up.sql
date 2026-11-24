-- P1-8 修复：ingest_tasks 增加 worker 归属列，重启恢复只清理「本实例」的残留。
--
-- 问题：旧实现 repo.FailZombieTasks / FailProcessingDocs 是无条件全库 UPDATE
-- （`WHERE status IN (1,2)` / `WHERE status = 2`），多实例部署下任一 worker 启动
-- 都会把其它实例**正在处理**的任务置失败，并把对应文档置失败（互相踩踏）。
--
-- 方案：任务在 worker 侧开始处理时写入自己的 worker_id（配置 WORKER_ID，缺省主机名），
-- 重启恢复时只处理 `worker_id = 本实例` 的行；历史行（本列 NULL）由启动恢复清理一次。
ALTER TABLE ingest_tasks ADD COLUMN IF NOT EXISTS worker_id VARCHAR(100);

-- 重启恢复按 (worker_id, status) 定位残留任务；与 idx_ingest_tasks_doc
-- (document_id, created_at DESC) 互补，不影响既有查询计划。
CREATE INDEX IF NOT EXISTS idx_ingest_tasks_worker_status ON ingest_tasks (worker_id, status);
