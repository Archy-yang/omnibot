# 架构说明：internal/db/database.go

## 模块职责
统一数据库连接管理模块，负责：
- 多驱动数据库连接初始化（SQLite/MySQL）
- 连接池配置与管理
- 数据迁移（AutoMigrate）
- 事务管理
- 健康检查
- 优雅关闭

## 已实现能力

| 能力 | 说明 |
|------|------|
| ✅ 多驱动支持 | SQLite（纯Go驱动）、预留MySQL |
| ✅ 连接池管理 | MaxOpenConns、MaxIdleConns、ConnMaxLifetime、ConnMaxIdleTime |
| ✅ 自动迁移 | GORM AutoMigrate 自动建表 |
| ✅ 健康检查 | 带超时的 Ping 检测 |
| ✅ 优雅关闭 | 5秒超时的连接关闭 |
| ✅ 事务封装 | 自动提交/回滚的事务支持 |
| ✅ 连接统计 | 获取连接池运行状态数据 |

## 入口函数

### `InitDB(cfg *config.DatabaseConfig, opts ...Option) (*Database, error)`
初始化数据库连接，配置连接池，执行自动迁移。

**参数：**
- `cfg` - 数据库配置（Driver、DSN、MaxConns）
- `opts` - 可选配置（如 SkipDefaultTransaction）

**返回：**
- `*Database` - 数据库实例包装

### `db.HealthCheck(ctx context.Context) error`
数据库健康检查，5秒超时。

### `db.Stats() map[string]interface{}`
获取连接池统计信息。

### `db.Close() error`
优雅关闭数据库连接。

### `db.Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error`
事务封装，自动提交/回滚。

### `db.GetGormDB() *gorm.DB`
获取原始 GORM 实例（供仓储层使用）。

## 配置参数

```yaml
database:
  driver: "sqlite"         # 数据库驱动: sqlite/mysql
  dsn: "data/app.db"       # 连接字符串
  max_conns: 25            # 最大连接数
```

## 依赖链

```
SetupRouter
    ↓
db.InitDB(cfg.Database) → *Database
    ↓
userRepo.NewUserRepository(db.GetGormDB())
wechatRepo.NewWechatAccountRepository(db.GetGormDB())
    ↓
userService.NewUserService(userRepo, wechatRepo)
    ↓
wechat.NewHandler(..., userService)
```

## 自动迁移表

- `user.User` - 用户核心表
- `user.WechatAccount` - 微信账号关联表

## 设计原则

1. **依赖注入友好**：通过构造函数传入依赖，便于测试 Mock
2. **分层解耦**：DB 层只负责连接管理，不包含业务逻辑
3. **生产就绪**：连接池、健康检查、优雅关闭
4. **可扩展**：预留 MySQL 驱动支持，便于后续扩展

## 未来扩展

- [ ] MySQL 驱动支持
- [ ] 版本化数据迁移（go-migrate）
- [ ] 读写分离支持
- [ ] 数据库审计日志
- [ ] 慢查询日志
