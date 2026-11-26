-- M11 知识库可见性（FR-17）：
--   visibility 1=私有（默认，仅成员可见，维持不可见原则）
--            2=全员可见（登录用户可浏览/检索/问答/点踩，等效只读成员；写操作仍按 R6 角色矩阵）
-- 现有库全部回填为私有（1），语义与升级前完全一致。
ALTER TABLE knowledge_bases
    ADD COLUMN visibility SMALLINT NOT NULL DEFAULT 1;
