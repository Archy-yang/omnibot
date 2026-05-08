# 测试计划：internal/db/database_test.go

## DB 模块集成测试

---
## 方法：TestInitDB_SQLite

**测试目的**：验证 SQLite 数据库初始化功能正常

**输入参数**：
- 数据库配置：`driver: "sqlite"`, `dsn: ":memory:"`, `max_conns: 5`

**期望断言**：
- 返回的 `*Database` 不为 nil
- 错误为 nil
- `Driver()` 返回 "sqlite"

---
## 方法：TestInitDB_PostgreSQL_Fallback

**测试目的**：验证 PostgreSQL 驱动可加载（不要求真实数据库连接）

**输入参数**：
- 数据库配置：`driver: "postgres"`, `dsn: "host=invalid port=1 invalid"`

**期望断言**：
- 应该返回连接错误（因为没有真实 PG 服务器）
- 不应该 panic 或驱动不支持的错误

---
## 方法：TestHealthCheck

**测试目的**：验证健康检查功能正常

**输入参数**：
- 使用内存 SQLite 初始化的 DB 实例
- `context.Background()`

**期望断言**：
- `HealthCheck()` 返回 nil 错误
- 数据库连接可用

---
## 方法：TestTransaction_Commit

**测试目的**：验证事务提交功能正常

**输入参数**：
- 使用内存 SQLite 初始化的 DB 实例
- 事务函数内执行 `CREATE TABLE test_tx (id INT)` 并插入一条数据

**期望断言**：
- `Transaction()` 返回 nil 错误
- 提交后可以查询到插入的数据

---
## 方法：TestTransaction_Rollback

**测试目的**：验证事务回滚功能正常

**输入参数**：
- 使用内存 SQLite 初始化的 DB 实例
- 事务函数内执行插入后返回错误触发回滚

**期望断言**：
- `Transaction()` 返回非 nil 错误
- 查询不到插入的数据（已回滚）

---
## 方法：TestStats

**测试目的**：验证连接池统计功能正常

**输入参数**：
- 使用内存 SQLite 初始化的 DB 实例

**期望断言**：
- `Stats()` 返回的 map 不为 nil
- 包含 `max_open_conns`, `open_conns`, `idle` 等 key
- 值类型正确

---
## 方法：TestClose

**测试目的**：验证优雅关闭功能正常

**输入参数**：
- 使用内存 SQLite 初始化的 DB 实例

**期望断言**：
- `Close()` 返回 nil 错误
- 关闭后再执行查询应该返回错误

---
## 方法：TestGetGormDB

**测试目的**：验证可以获取原始 GORM 实例供仓储层使用

**输入参数**：
- 使用内存 SQLite 初始化的 DB 实例

**期望断言**：
- `GetGormDB()` 返回非 nil 的 `*gorm.DB`
- 可以用返回的 DB 实例执行查询
