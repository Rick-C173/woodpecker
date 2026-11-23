-- M4 摄取管道：文件内容指纹（增量同步 diff 用）
ALTER TABLE document_files ADD COLUMN content_hash VARCHAR(64);

-- 重启中断的遗留任务按失败处理（FR-06：系统重启后，中断任务按失败处理，可手动重试）
-- 由代码在服务启动时执行 FailZombieTasks / FailProcessingDocs，无需数据订正。
