-- M14 Git 自动同步（FR-03 增强）：
--   documents.auto_sync       定时拉取开关：true=worker 周期扫描并入队增量 sync（间隔 GIT_AUTO_SYNC_MINUTES）
--   documents.webhook_secret  webhook 验签 token（AES-GCM 加密落库；NULL=未启用 webhook）。
--                             GitLab/GitHub 均支持自定义 header，统一用 X-Woodpecker-Token 携带；
--                             增量比对由 sync 任务本身完成（last_commit diff），webhook 仅是触发信号。
ALTER TABLE documents ADD COLUMN auto_sync BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE documents ADD COLUMN webhook_secret BYTEA;
