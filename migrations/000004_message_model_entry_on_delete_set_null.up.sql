-- P1-7 修复：messages.model_entry_id 外键改为 ON DELETE SET NULL。
--
-- 000001_init 里 `model_entry_id BIGINT REFERENCES model_entries(id)` 未指定 ON DELETE，
-- 默认 NO ACTION：只要该条目被任何一条 assistant 消息引用过（问答溯源），
-- DELETE /admin/model-entries/{id} 就触发外键冲突，被 FailServiceError 兜成 500「服务内部错误」
-- —— 跑过一次问答的旧模型条目永远删不掉，模型配置页留下永久垃圾数据。
--
-- 溯源语义不受影响：该列只是「本次回答所用模型」的快照引用，引用内容由 message_citations 承担，
-- 条目被删除后置空即代表「来源条目已下线」。
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_model_entry_id_fkey;

ALTER TABLE messages ADD CONSTRAINT messages_model_entry_id_fkey
    FOREIGN KEY (model_entry_id) REFERENCES model_entries(id) ON DELETE SET NULL;
