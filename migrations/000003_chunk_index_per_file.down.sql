-- 回退为「文档内全局唯一序号」。
-- 注意：若已按文件内序号写入过多文件分块，本语句会因唯一键冲突而失败，
-- 需先重建索引（DELETE 后重跑全量摄取）再回退。
ALTER TABLE chunks DROP CONSTRAINT IF EXISTS chunks_document_id_file_path_chunk_index_key;

ALTER TABLE chunks ADD CONSTRAINT chunks_document_id_chunk_index_key
    UNIQUE (document_id, chunk_index);
