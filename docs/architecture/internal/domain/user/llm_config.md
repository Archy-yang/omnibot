# LLMConfig 领域实体

## 模块职责
提供用户自定义 LLM 配置的领域实体，封装 LLM 相关配置数据及业务逻辑。

## 入口函数/方法

### 结构体
- `LLMConfig` - 用户自定义 LLM 配置实体

### 常量
- `LLMConfigStatusNormal` - 正常状态 (0)
- `LLMConfigStatusDisabled` - 禁用状态 (1)

### 方法
- `TableName()` - 返回数据库表名 "user_llm_configs"
- `IsEnabled()` - 判断配置是否启用（状态正常且 APIKey 非空）
- `GetBaseURL()` - 获取实际使用的 API 地址（nil 或空时返回默认值）
- `GetModel()` - 获取实际使用的模型名（nil 或空时返回默认值）

## 处理流程

### IsEnabled 判断流程
1. 检查 Status 是否为 Normal (0)
2. 检查 APIKey 是否非空
3. 两个条件都满足返回 true，否则返回 false

### GetBaseURL 取值流程
1. 若 BaseURL 为 nil 或指向空字符串，返回默认值 "https://api.openai.com/v1"
2. 否则返回自定义的 BaseURL

### GetModel 取值流程
1. 若 Model 为 nil 或指向空字符串，返回默认值 "gpt-3.5-turbo"
2. 否则返回自定义的 Model

## 已实现能力
- 用户 LLM 配置数据结构定义（含 GORM 标签）
- 配置启用状态判断
- 默认值 fallback 逻辑（BaseURL、Model）
- 数据库表名自定义

## 依赖关系
- 无外部依赖
