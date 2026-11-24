-- 回退为默认 NO ACTION（恢复 000001_init 的原始行为）。
--
-- 说明：外键约束本身允许 NULL，因此已有的 NULL 值（历史回答的模型条目已被删除）不会阻塞本回退；
-- 但回退后「删除被引用条目」会重新退化为 500 外键冲突。
-- 若希望把 NULL 回填为现存条目（更接近回退前的语义），可在回退后执行：
--   -- UPDATE messages SET model_entry_id = (SELECT MIN(id) FROM model_entries)
--   --  WHERE model_entry_id IS NULL AND EXISTS (SELECT 1 FROM model_entries);
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_model_entry_id_fkey;

ALTER TABLE messages ADD CONSTRAINT messages_model_entry_id_fkey
    FOREIGN KEY (model_entry_id) REFERENCES model_entries(id);
