-- Woodpecker 初始表结构（v1 合并基线，2026-11-26 终态 schema，含 M15 API Token）。
-- ⚠ 迁移纪律（migrations.go / 数据库设计.md 头部）：
--   1. 本文件为基线：功能开发期可临时新增 schema_patch_<feature>.up.sql（"." 字典序小于
--      "_"，保证基线先执行），功能验证完毕把 patch 内容合并回本文件并删除 patch 文件；
--   2. 再次合并的唯一条件：所有部署环境均可 DROP 重建（无生产数据窗口期），人工决策执行；
--      一旦出现生产数据，合并窗口永久关闭，转为只增不改；
--   3. chunks 引用 document_files、messages 引用 model_entries，建表顺序不可调换。

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- 用户与权限（PRD R6）：email 为登录身份之一（FR-16）与 OIDC 身份锚点（FR-19）；
-- auth_source 1=本地密码 2=OIDC（OIDC 建号账号无本地密码，密码位为随机不可登录哈希）
CREATE TABLE users (
    id                   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username             VARCHAR(64)  NOT NULL UNIQUE,
    password_hash        VARCHAR(255) NOT NULL,
    email                VARCHAR(255),                    -- 唯一（部分索引），可空
    is_admin             BOOLEAN      NOT NULL DEFAULT FALSE,
    status               SMALLINT     NOT NULL DEFAULT 1,  -- 1=启用 2=禁用
    must_change_password BOOLEAN      NOT NULL DEFAULT FALSE,
    token_version        INT          NOT NULL DEFAULT 0,
    failed_attempts      INT          NOT NULL DEFAULT 0,
    locked_until         TIMESTAMPTZ,
    auth_source          SMALLINT     NOT NULL DEFAULT 1,  -- 1=本地密码 2=OIDC
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_users_email ON users(email) WHERE email IS NOT NULL;

-- 模型服务配置（PRD FR-12），先于 messages 建表
CREATE TABLE model_services (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        VARCHAR(100) NOT NULL,
    base_url    VARCHAR(500) NOT NULL,
    api_key_enc BYTEA        NOT NULL,
    enabled     BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE model_entries (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service_id BIGINT   NOT NULL REFERENCES model_services(id) ON DELETE CASCADE,
    purpose    SMALLINT NOT NULL, -- 1=chat 2=embedding 3=rerank
    model_name VARCHAR(200) NOT NULL,
    params     JSONB     NOT NULL DEFAULT '{}',
    is_default BOOLEAN  NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_model_entries_default ON model_entries(purpose) WHERE is_default;

-- 知识库：visibility 1=私有（仅成员）2=全员可见（登录用户等效只读成员，FR-17）
CREATE TABLE knowledge_bases (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        VARCHAR(50)  NOT NULL,
    description VARCHAR(500),
    visibility  SMALLINT     NOT NULL DEFAULT 1,
    created_by  BIGINT       NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE kb_members (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kb_id      BIGINT   NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    user_id    BIGINT   NOT NULL REFERENCES users(id),
    role       SMALLINT NOT NULL, -- 1=管理者(owner) 2=编辑者(editor) 3=成员(member)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kb_id, user_id)
);
CREATE INDEX idx_kb_members_user ON kb_members(user_id);

-- 用户组（FR-18）：auto=true 的组由 OIDC groups claim 同步（FR-19）；
-- created_by 无 users 外键（仅审计信息）：0=系统自动创建（OIDC 组同步）
CREATE TABLE user_groups (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(50) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    auto        BOOLEAN NOT NULL DEFAULT FALSE,
    created_by  BIGINT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_user_groups_name UNIQUE (name)
);

CREATE TABLE group_members (
    id         BIGSERIAL PRIMARY KEY,
    group_id   BIGINT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_group_members UNIQUE (group_id, user_id)
);
CREATE INDEX idx_group_members_user ON group_members(user_id);

-- 组 → 库的角色授权（role 与 kb_members 同枚举）；有效角色 = max(直授, 组授)
CREATE TABLE kb_group_roles (
    id         BIGSERIAL PRIMARY KEY,
    kb_id      BIGINT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    group_id   BIGINT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    role       SMALLINT NOT NULL, -- 1=管理者(owner) 2=编辑者(editor) 3=成员(member)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_kb_group_roles UNIQUE (kb_id, group_id)
);
CREATE INDEX idx_kb_group_roles_group ON kb_group_roles(group_id);

-- 邀请注册（FR-16）：每邮箱一条，重复邀请覆盖 token（重发）；
-- token 只存 sha256 哈希（URL 明文仅出现在邮件与创建响应中，库泄漏不泄露可用链接）
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

-- M14 Git 自动同步（FR-03 增强）：auto_sync=true 时 worker 周期扫描入队增量 sync
-- （间隔 GIT_AUTO_SYNC_MINUTES）；webhook_secret 为 AES-GCM 加密验签 token
-- （Git 平台自定义 header X-Woodpecker-Token 比对，NULL=未启用）
CREATE TABLE documents (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kb_id          BIGINT   NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    type           SMALLINT NOT NULL, -- 1=git 2=upload 3=url
    name           VARCHAR(500) NOT NULL,
    status         SMALLINT NOT NULL DEFAULT 1, -- 1=待处理 2=处理中 3=已就绪 4=失败
    stage          SMALLINT, -- status=2 时：1=解析 2=切块 3=向量化
    error          TEXT,
    repo_url       VARCHAR(1000),
    git_ref        VARCHAR(200),
    credential_enc BYTEA,
    ignore_rules   TEXT[]       DEFAULT '{}',
    last_commit    VARCHAR(40),
    last_synced_at TIMESTAMPTZ,
    auto_sync      BOOLEAN NOT NULL DEFAULT false,
    webhook_secret BYTEA,
    source_url     VARCHAR(2000),
    file_count     INT,
    total_size     BIGINT,
    created_by     BIGINT   NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_documents_kb_status ON documents(kb_id, status);

CREATE TABLE document_files (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id  BIGINT   NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    path         VARCHAR(1000) NOT NULL,
    status       SMALLINT NOT NULL DEFAULT 1, -- 1=已索引 2=已排除 3=失败
    language     VARCHAR(50),
    size         BIGINT,
    error        TEXT,
    content_hash VARCHAR(64), -- 内容指纹（增量同步 diff 用）
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (document_id, path)
);
CREATE INDEX idx_document_files_doc_status ON document_files(document_id, status);

-- chunk_index 语义为「文件内序号」：增量同步按文件替换分块后各文件从 0 重新编号，
-- 故唯一键含 file_path（原 (document_id, chunk_index) 全局唯一键会冲突）。
CREATE TABLE chunks (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id BIGINT   NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    kb_id       BIGINT   NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    file_id     BIGINT   REFERENCES document_files(id) ON DELETE CASCADE,
    content     TEXT     NOT NULL,
    chunk_index INT      NOT NULL,
    token_count INT,
    file_path    VARCHAR(1000),
    line_start   INT,
    line_end     INT,
    heading_path TEXT,
    embedding   vector(1024) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chunks_document_id_file_path_chunk_index_key
        UNIQUE (document_id, file_path, chunk_index)
);
CREATE INDEX idx_chunks_hnsw ON chunks
    USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX idx_chunks_trgm ON chunks USING gin (content gin_trgm_ops);
CREATE INDEX idx_chunks_kb ON chunks(kb_id);

CREATE TABLE conversations (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kb_id      BIGINT   NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    user_id    BIGINT   NOT NULL REFERENCES users(id),
    title      VARCHAR(200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_conversations_user ON conversations(user_id, updated_at DESC);

CREATE TABLE messages (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id BIGINT   NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role            SMALLINT NOT NULL, -- 1=user 2=assistant
    content         TEXT     NOT NULL,
    interrupted     BOOLEAN  NOT NULL DEFAULT FALSE,
    prompt_tokens     INT,
    completion_tokens INT,
    -- 「本次回答所用模型」快照引用；条目下线置空，溯源由 message_citations 承担。
    -- ON DELETE SET NULL：否则删除被引用过的模型条目会外键冲突 500。
    model_entry_id  BIGINT REFERENCES model_entries(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_messages_conv ON messages(conversation_id, created_at);

CREATE TABLE message_citations (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id  BIGINT   NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    document_id BIGINT,
    seq         INT      NOT NULL,
    chunk_id    BIGINT,
    file_path   VARCHAR(1000),
    line_start  INT,
    line_end    INT,
    heading_path TEXT,
    source_url  VARCHAR(2000),
    snippet     TEXT
);
CREATE INDEX idx_citations_message ON message_citations(message_id, seq);

CREATE TABLE feedbacks (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    message_id BIGINT   NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    user_id    BIGINT   NOT NULL REFERENCES users(id),
    reason     SMALLINT NOT NULL, -- 1=引用不相关 2=内容错误 3=没有找到想要的 4=其他
    comment    VARCHAR(500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (message_id, user_id)
);

-- 摄取任务（FR-06）：重启恢复依赖 worker_id 归属 + heartbeat_at 时间兜底，缺一不可——
-- 仅按 worker_id 判定时跨重启标识变化（k8s 主机名必变）会留下永久 running 的僵尸任务；
-- 仅按时间判定时无法区分「正常长任务」与「重启残留」，归属列仍是启动恢复的第一依据。
CREATE TABLE ingest_tasks (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id  BIGINT   NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    type         SMALLINT NOT NULL, -- 1=全量摄取 2=增量同步
    status       SMALLINT NOT NULL DEFAULT 1, -- 1=排队 2=运行 3=成功 4=失败
    retries      INT      NOT NULL DEFAULT 0,
    error        TEXT,
    payload      JSONB,
    worker_id    VARCHAR(100), -- 处理实例标识（WORKER_ID，缺省主机名）
    heartbeat_at TIMESTAMPTZ,  -- 运行中心跳（默认 30s 刷新），超时视为僵尸
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);
CREATE INDEX idx_ingest_tasks_doc ON ingest_tasks(document_id, created_at DESC);
CREATE INDEX idx_ingest_tasks_worker_status ON ingest_tasks (worker_id, status);
CREATE INDEX idx_ingest_tasks_heartbeat ON ingest_tasks (status, heartbeat_at);
-- 同一文档最多一个排队/运行中任务（并发重试/同步双写防护）；
-- 应用层捕获 23505 → DOC_TASK_RUNNING（409）。
CREATE UNIQUE INDEX uq_ingest_tasks_running
    ON ingest_tasks (document_id)
    WHERE status IN (1, 2);

CREATE TABLE audit_logs (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     BIGINT,
    action      VARCHAR(50) NOT NULL,
    object_type VARCHAR(30),
    object_id   BIGINT,
    detail      JSONB,
    ip          VARCHAR(45),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_created ON audit_logs(created_at DESC);
CREATE INDEX idx_audit_user    ON audit_logs(user_id, created_at DESC);
CREATE INDEX idx_audit_object  ON audit_logs(object_type, object_id);

CREATE TABLE settings (
    key        VARCHAR(50) PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by BIGINT REFERENCES users(id)
);
-- 个人 API Token（机器访问凭证，脚本/CI/CLI 鉴权；MCP Server 前置依赖）：
-- 仅存 SHA-256 哈希（明文 wpk_<64hex> 仅创建响应返回一次）；prefix 为 64hex 前 8 字符
-- （列表展示时拼 wpk_ 前缀）；token 权限=所属用户本人权限，禁用即时失效；
-- 不受 users.token_version 影响（改密不使机器凭证失效）。MVP 无细粒度 scope（二期扩展）。
CREATE TABLE api_tokens (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      BIGINT       NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         VARCHAR(100) NOT NULL,
    token_hash   CHAR(64)     NOT NULL UNIQUE,
    prefix       CHAR(8)      NOT NULL,
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,                 -- NULL=永久
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ                  -- NULL=有效；软吊销
);
CREATE INDEX idx_api_tokens_user ON api_tokens(user_id, created_at DESC);

