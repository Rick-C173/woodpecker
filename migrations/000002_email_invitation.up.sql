-- FR-16 邮箱邀请制注册：
--   users 增加可选 email（唯一，登录支持用户名或邮箱；存量账号 email 为空）；
--   invites 邀请表：每邮箱一条记录，重复邀请覆盖 token（重发）；
--   token 只存 sha256 哈希（URL 明文仅出现在邮件与创建响应中，库泄漏不泄露可用链接）。

ALTER TABLE users ADD COLUMN email VARCHAR(255);
CREATE UNIQUE INDEX uq_users_email ON users(email) WHERE email IS NOT NULL;

CREATE TABLE invites (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email        VARCHAR(255) NOT NULL UNIQUE,          -- 统一小写
    token_hash   VARCHAR(64)  NOT NULL UNIQUE,          -- sha256 hex(token)
    status       SMALLINT     NOT NULL DEFAULT 1,       -- 1=待接受 2=已接受 3=已撤销
    expires_at   TIMESTAMPTZ  NOT NULL,                 -- 7 天有效期（重发时重置）
    accepted_at  TIMESTAMPTZ,
    accepted_by  BIGINT REFERENCES users(id),
    send_status  SMALLINT     NOT NULL DEFAULT 3,       -- 1=已发送 2=发送失败 3=未发送（SMTP 未配置）
    send_error   TEXT,
    send_count   INT          NOT NULL DEFAULT 0,       -- 实际发送尝试次数
    created_by   BIGINT       NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX idx_invites_status ON invites(status, created_at DESC);
