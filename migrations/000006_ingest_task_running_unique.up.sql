-- P1-10 修复：同一文档最多一个「排队/运行中」的摄取任务，正确性由 DB 唯一索引兜底。
--
-- 问题：service.enqueueNewTask 的 HasRunningTask 是「先查后写」，并发触发重试/同步/重建时
-- 两个请求都能通过检查并各自 CreateTask → 同一文档出现两条运行中任务，两个 worker 同时
-- UpsertFile + InsertChunks，导致分块与文件清单半新半旧。
--
-- 步骤 1：先清理迁移前的重复运行中任务——同一 document_id 只保留 id 最小的一条，
-- 其余置为失败（status=4）。不先清理，下面建唯一索引会直接失败。
UPDATE ingest_tasks
SET status      = 4,
    error       = '迁移前清理：运行中任务重复',
    finished_at = now()
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               row_number() OVER (PARTITION BY document_id ORDER BY id) AS rn
        FROM ingest_tasks
        WHERE status IN (1, 2)
    ) t
    WHERE t.rn > 1
);

-- 步骤 2：部分唯一索引，只约束排队(1)/运行(2)；已结束任务不受限（历史允许多条）。
-- 应用层捕获该约束的 23505 冲突 → 返回既有业务错误 DOC_TASK_RUNNING（409）。
CREATE UNIQUE INDEX IF NOT EXISTS uq_ingest_tasks_running
    ON ingest_tasks (document_id)
    WHERE status IN (1, 2);
