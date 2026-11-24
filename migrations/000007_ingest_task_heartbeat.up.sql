-- P1（验收回归 8-1）：ingest_tasks 增加心跳列，重启恢复用「时间兜底」回收跨实例僵尸任务。
--
-- 问题：000005 的归属判定是 `worker_id = 本实例`，一旦 worker 标识跨重启变化
-- （WORKER_ID 缺省取主机名，k8s 每次重建 Pod 主机名必变），上一实例留下的
-- `status=2(running) + worker_id=旧值` 两侧都不命中 → 任务永远停在 running、
-- 文档永远停在 processing，uq_ingest_tasks_running 又挡住 retry/sync/reindex，
-- 用户唯一出路是删文档重建。
--
-- 方案：worker 处理任务期间周期性刷新 heartbeat_at（默认 30s，见 worker 包），
-- 启动恢复额外清理「运行中且心跳（或 started_at/created_at）超过阈值未更新」的任务，
-- 不再依赖 worker_id 是否与上一实例一致。
ALTER TABLE ingest_tasks ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;

-- 时间兜底扫描按 (status, heartbeat_at) 过滤运行中任务；已结束任务不参与判定。
CREATE INDEX IF NOT EXISTS idx_ingest_tasks_heartbeat ON ingest_tasks (status, heartbeat_at);
