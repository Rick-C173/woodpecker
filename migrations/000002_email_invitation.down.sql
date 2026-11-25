-- 回退 FR-16：删除邀请表与 users.email 列。
DROP TABLE IF EXISTS invites;
DROP INDEX IF EXISTS uq_users_email;
ALTER TABLE users DROP COLUMN IF EXISTS email;
