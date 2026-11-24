-- 回退 000007：删除心跳列与索引。
--
-- 说明：回退后重启恢复只剩「按 worker_id 归属」一段判定，worker 标识跨重启变化时
-- 上一实例的 running 任务将再次永久卡死（验收回归 8-1 缺陷回归）。
DROP INDEX IF EXISTS idx_ingest_tasks_heartbeat;

ALTER TABLE ingest_tasks DROP COLUMN IF EXISTS heartbeat_at;
