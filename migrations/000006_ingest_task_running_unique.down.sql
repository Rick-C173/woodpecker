-- 回退 000006：删除「同文档运行中任务唯一」约束。
--
-- 说明：回退后并发「重试/同步/重建」仍可能产生双任务（P1-10 缺陷回归）；
-- 被本迁移置为失败的重复任务（error = '迁移前清理：运行中任务重复'）不会自动恢复，
-- 如确需恢复可执行：
--   -- UPDATE ingest_tasks SET status = 1, error = NULL, finished_at = NULL
--   --  WHERE error = '迁移前清理：运行中任务重复';
DROP INDEX IF EXISTS uq_ingest_tasks_running;
