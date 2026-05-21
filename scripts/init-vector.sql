-- 启用 pgvector 扩展
CREATE EXTENSION IF NOT EXISTS vector;

-- 创建向量相似度搜索函数（可选）
CREATE OR REPLACE FUNCTION cosine_similarity(v1 vector, v2 vector)
RETURNS float8 AS $$
BEGIN
  RETURN 1 - (v1 <=> v2);
END;
$$ LANGUAGE plpgsql IMMUTABLE;

-- 创建向量索引函数示例（供后续使用）
-- CREATE INDEX ON messages USING hnsw (embedding vector_cosine_ops);
