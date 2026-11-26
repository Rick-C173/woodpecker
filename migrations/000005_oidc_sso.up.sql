-- M13 企业单点登录 SSO / OIDC（FR-19）：
--   users.auth_source   账号来源：1=本地密码（默认）2=OIDC（无本地密码）
--   user_groups.auto    组自动同步标志：true=登录时按 groups claim 双向对齐；false=手动组不参与同步
ALTER TABLE users ADD COLUMN auth_source SMALLINT NOT NULL DEFAULT 1;
ALTER TABLE user_groups ADD COLUMN auto BOOLEAN NOT NULL DEFAULT false;
-- OIDC 组同步自动建组无真实创建者（created_by=0 表示系统创建），故放开 users 外键；
-- created_by 仅作审计信息，repo 查询不依赖该外键。
ALTER TABLE user_groups DROP CONSTRAINT IF EXISTS user_groups_created_by_fkey;
