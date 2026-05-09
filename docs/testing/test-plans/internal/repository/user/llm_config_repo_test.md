# LLMConfig Repository 测试计划

## 概述
测试 LLM 配置仓储层的 CRUD 操作，确保数据持久化正确性。

---
## 方法：TestLLMConfigRepository_CreateAndGet

**测试目的**：验证创建配置后能正确读取

**输入参数**：
- 创建包含完整字段的 LLMConfig
- 通过 UserID 查询

**期望断言**：
- Create 无错误
- 生成的 ID > 0
- 查询到的配置字段与创建时一致

---
## 方法：TestLLMConfigRepository_GetNotFound

**测试目的**：验证查询不存在的配置时返回 ErrRecordNotFound

**输入参数**：
- 查询不存在的 UserID (999)

**期望断言**：
- 返回错误为 gorm.ErrRecordNotFound

---
## 方法：TestLLMConfigRepository_Update

**测试目的**：验证配置更新功能

**输入参数**：
- 创建配置
- 修改 APIKey 和 BaseURL
- 调用 Update

**期望断言**：
- Update 无错误
- 更新后读取到新值

---
## 方法：TestLLMConfigRepository_Delete

**测试目的**：验证配置删除功能

**输入参数**：
- 创建配置
- 按 UserID 删除

**期望断言**：
- Delete 无错误
- 删除后查询不到记录

---
## 方法：TestLLMConfigRepository_UserUnique

**测试目的**：验证 UserID 唯一约束生效

**输入参数**：
- 对同一 UserID 创建两条配置

**期望断言**：
- 第二次 Create 返回错误
