-- v1 合并基线回滚：全量重建（数据不可恢复）。
-- ⚠ 仅在确认环境可整体重建时执行——生产环境永远不需要回滚到"无库"状态。
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS ingest_tasks;
DROP TABLE IF EXISTS feedbacks;
DROP TABLE IF EXISTS message_citations;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS document_files;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS invites;
DROP TABLE IF EXISTS kb_group_roles;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS user_groups;
DROP TABLE IF EXISTS kb_members;
DROP TABLE IF EXISTS knowledge_bases;
DROP TABLE IF EXISTS model_entries;
DROP TABLE IF EXISTS model_services;
DROP TABLE IF EXISTS users;
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS vector;
