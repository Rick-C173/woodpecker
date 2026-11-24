-- P0-1 修复：chunk_index 语义由「文档内全局序号」改为「文件内序号」。
-- 增量同步按文件替换分块后，各文件都从 0 重新编号，原有的
-- UNIQUE (document_id, chunk_index) 会让第二个文件的第 0 块与首个文件冲突，
-- 故唯一键补入 file_path：UNIQUE (document_id, file_path, chunk_index)。
-- 旧数据是全局唯一序号，必然满足「同一文件内唯一」，无需回填。
ALTER TABLE chunks DROP CONSTRAINT IF EXISTS chunks_document_id_chunk_index_key;

ALTER TABLE chunks ADD CONSTRAINT chunks_document_id_file_path_chunk_index_key
    UNIQUE (document_id, file_path, chunk_index);
