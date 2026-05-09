# LLMConfig Repository 架构文档

## 模块职责
提供用户自定义 LLM 配置的持久化操作接口，封装 GORM 实现。

## 接口定义
```go
type LLMConfigRepository interface {
    GetByUserID(userID int64) (*user.LLMConfig, error)
    Create(config *user.LLMConfig) error
    Update(config *user.LLMConfig) error
    Delete(userID int64) error
}
```

## 入口函数
- `NewLLMConfigRepository(db *gorm.DB) LLMConfigRepository`
  - 输入：GORM 数据库连接
  - 输出：LLMConfigRepository 接口实例

## 处理流程

### GetByUserID
1. 通过 `user_id` 查询 `user_llm_configs` 表
2. 返回查询结果或 `gorm.ErrRecordNotFound`

### Create
1. 插入新记录到 `user_llm_configs` 表
2. 自动填充 `created_at`、`updated_at` 字段
3. 约束：`user_id` 唯一索引，同一用户不能有多条配置

### Update
1. 通过主键更新完整记录
2. 自动更新 `updated_at` 字段

### Delete
1. 通过 `user_id` 软删除（GORM 默认不开启，此处为物理删除）

## 已实现能力
- ✅ 按用户ID查询配置
- ✅ 创建用户配置（含唯一约束）
- ✅ 更新用户配置
- ✅ 删除用户配置

## 依赖关系
- Domain 层：`internal/domain/user.LLMConfig`
- 外部依赖：`gorm.io/gorm`
