-- M12 用户组与组授权（FR-18）：
--   user_groups     组本身（name 全局唯一）
--   group_members   组员（双 CASCADE：删组/删用户级联清除）
--   kb_group_roles  组 → 库的角色授权（role 与 kb_members 同枚举 1=owner 2=editor 3=member）
-- 生效规则：库内有效角色 = 直接授权(kb_members) 与组授权(kb_group_roles ∩ group_members) 的最高者。
CREATE TABLE user_groups (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(50) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    created_by  BIGINT NOT NULL REFERENCES users(id),
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

CREATE TABLE kb_group_roles (
    id         BIGSERIAL PRIMARY KEY,
    kb_id      BIGINT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    group_id   BIGINT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    role       SMALLINT NOT NULL, -- 1=管理者(owner) 2=编辑者(editor) 3=成员(member)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_kb_group_roles UNIQUE (kb_id, group_id)
);

CREATE INDEX idx_group_members_user ON group_members(user_id);
CREATE INDEX idx_kb_group_roles_group ON kb_group_roles(group_id);
