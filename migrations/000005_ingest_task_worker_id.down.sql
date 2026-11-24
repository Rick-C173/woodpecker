-- 回退 000005：删除 worker 归属列与索引。
--
-- 说明：回退后重启恢复退化为「全库清理」（P1-8 缺陷回归）——多实例部署请勿单独回退本迁移。
DROP INDEX IF EXISTS idx_ingest_tasks_worker_status;

ALTER TABLE ingest_tasks DROP COLUMN IF EXISTS worker_id;
