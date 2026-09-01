# PostgreSQL + pgvector 向量数据库方案

> **状态（2026-08-31）**：本文档为**参考资料，当前方案未采用**。记忆系统的向量方案见《[12-记忆系统技术方案](30-服务架构/01-高层设计/12-记忆系统技术方案.md)》：
> 现阶段用 JSON 向量列 + 应用层余弦（语料千级足够，SQLite/PG 通吃）；本文档保留为语料过万后演进 pgvector 时的参考。

## 概述

本项目使用 PostgreSQL 16 + pgvector 0.8.0 作为向量数据库，支持：
- 向量存储和相似度搜索
- HNSW / IVFFlat 索引加速查询
- 完整的 SQL 查询能力

## 快速启动

### 方式一：仅启动 PostgreSQL

```bash
make db-up
```

### 方式二：启动 PostgreSQL + Redis

```bash
make db-up-all
```

### 停止服务

```bash
make db-down
```

### 查看日志

```bash
make db-logs
```

## 连接配置

### DSN 格式

```
host=localhost user=omnibot password=omnibot123 dbname=omnibot port=5432 sslmode=disable
```

### Go 代码中使用

```go
import (
    "gorm.io/driver/postgres"
    "gorm.io/gorm"
)

dsn := "host=localhost user=omnibot password=omnibot123 dbname=omnibot port=5432 sslmode=disable"
db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
```

## 集成测试

PostgreSQL 集成测试使用 Testcontainers 启动临时 `pgvector/pgvector:0.8.0-pg16` 容器，不依赖常驻数据库。

### 运行 PostgreSQL 集成测试

```bash
make test-postgres
```

等价于：

```bash
CGO_ENABLED=0 go test -tags=postgres_integration -v ./internal/... ./cmd/...
```

### 编写 PostgreSQL 集成测试

测试文件需要添加 build tag：

```go
//go:build postgres_integration
```

使用测试辅助函数创建真实 PostgreSQL 连接：

```go
func TestSomethingWithPostgres(t *testing.T) {
    db := NewPostgresTestDB(t)
    // 使用 db 执行 GORM 测试
}
```

测试容器会在测试结束后自动销毁。默认快速测试 `make test` 不会启动 Docker。

### 迁移顺序约束

`InitDB` 在 PostgreSQL 模式下会先执行 `CREATE EXTENSION IF NOT EXISTS vector`，再执行 GORM AutoMigrate。这样后续新增 `gorm:"type:vector(...)"` 字段时，迁移阶段可以直接使用 `vector` 类型。

## pgvector 使用示例

### 1. 创建向量字段

```go
type Message struct {
    ID        int64
    Content   string
    Embedding []float32 `gorm:"type:vector(1536)"` // OpenAI ada-002 维度
}
```

### 2. 相似度查询

```go
// 余弦相似度搜索
var results []Message
db.Raw(`
    SELECT *, 1 - (embedding <=> ?) as similarity
    FROM messages
    ORDER BY embedding <=> ?
    LIMIT 10
`, queryVector, queryVector).Scan(&results)
```

### 3. 创建索引

```sql
-- HNSW 索引（适合高维向量，查询快）
CREATE INDEX ON messages USING hnsw (embedding vector_cosine_ops);

-- IVFFlat 索引（适合大数据集，构建快）
CREATE INDEX ON messages USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 100);
```

## 配置说明

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| POSTGRES_USER | omnibot | 数据库用户名 |
| POSTGRES_PASSWORD | omnibot123 | 数据库密码 |
| POSTGRES_DB | omnibot | 数据库名 |

### 端口映射

| 服务 | 端口 |
|------|------|
| PostgreSQL | 5432 |
| Redis | 6379 |

## 数据持久化

Docker volumes 用于数据持久化：
- `postgres_data`: PostgreSQL 数据目录
- `redis_data`: Redis 数据目录

### 清除所有数据（危险！）

```bash
make db-purge
```

## 切换数据库驱动

修改 `configs/config.yaml`:

```yaml
database:
  driver: "postgres"  # 从 sqlite 改为 postgres
  dsn: "host=localhost user=omnibot password=omnibot123 dbname=omnibot port=5432 sslmode=disable"
  max_conns: 25
```

## 向量维度参考

| 模型 | 维度 |
|------|------|
| OpenAI text-embedding-ada-002 | 1536 |
| OpenAI text-embedding-3-small | 1536 |
| OpenAI text-embedding-3-large | 3072 |
| 通义千问 text-embedding-v1 | 1536 |
| 豆包 embedding | 1024 |

## 性能调优

### PostgreSQL 配置

在 `docker-compose.yml` 中添加命令参数：

```yaml
command:
  - postgres
  - -c
  - shared_buffers=256MB
  - -c
  - work_mem=32MB
  - -c
  - maintenance_work_mem=64MB
```

### 索引选择

- **HNSW**: 查询速度快，内存占用高，构建慢 - 适合查询密集场景
- **IVFFlat**: 构建快，内存占用低，需要调优 `lists` 参数 - 适合写入密集场景

## 诊断命令

```bash
# 查看 pgvector 版本
docker exec -it omnibot-postgres psql -U omnibot -c "SELECT * FROM pg_extension WHERE extname = 'vector';"

# 查看向量索引
docker exec -it omnibot-postgres psql -U omnibot -c "\di+"

# 查看表结构
docker exec -it omnibot-postgres psql -U omnibot -c "\d+ messages"
```

## 来源

- [pgvector 官方文档](https://github.com/pgvector/pgvector)
- [pgvector Docker 镜像](https://hub.docker.com/r/pgvector/pgvector)
