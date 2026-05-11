# 变更日志

本文件记录项目所有重要版本的变更内容。

---

## [v1.1.0] - 2026-05-10

### ✨ 新增功能

- **用户自定义 LLM 配置**
  - 支持用户通过微信命令配置自己的 API Key
  - 支持自定义 API 地址（兼容所有 OpenAI 格式的服务）
  - 配置命令：`#模型设置`、`#设置Key sk-xxx`、`#设置地址 https://xxx`、`#我的配置`、`#重置模型`
  - 配置查看时 API Key 脱敏展示（只显示前后3位）
  - 重置配置后自动恢复使用系统默认模型

- **AES-256-GCM 加密模块**
  - 用户 API Key 采用 AES-256-GCM 认证加密存储
  - 数据库中无明文密钥
  - 支持环境变量 `LLM_CONFIG_ENCRYPT_KEY` 指定加密密钥
  - 每次加密使用随机 nonce，相同明文加密结果不同

- **LLM 配置 Repository 层**
  - `Create(config *LLMConfig) error` - 创建配置
  - `GetByUserID(userID int64) (*LLMConfig, error)` - 查询用户配置
  - `Update(config *LLMConfig) error` - 更新配置
  - `Delete(userID int64) error` - 删除配置
  - user_id 唯一索引，每个用户只能有一份配置

- **LLM 配置 Service 层**
  - `SetAPIKey(userID int64, apiKey string) error` - 设置 API Key（自动加密）
  - `SetBaseURL(userID int64, baseURL string) error` - 设置 API 地址
  - `GetConfigForUser(userID int64)` - 获取解密后的配置（LLM 调用时使用）
  - `GetConfigView(userID int64)` - 获取配置视图（API Key 脱敏，用于前端展示）
  - `ClearConfig(userID int64) error` - 清除用户自定义配置

### 🔧 架构改进

- **配置注入机制**：LLM 调用时优先使用用户自定义配置，配额独立
- **错误友好提示**：配置调用失败时返回用户可理解的错误信息
- **向后兼容**：无配置用户继续使用系统默认模型，不影响现有功能

### 📦 数据变更

- 新增 `user_llm_configs` 表存储用户自定义 LLM 配置
- 包含字段：`id`、`user_id`、`api_key`（加密）、`base_url`、`model`、`status`、`created_at`、`updated_at`
- `user_id` 唯一索引

### 🔐 安全增强

- AES-256-GCM 认证加密存储敏感数据
- 加密密钥通过环境变量配置，不提交代码
- 数据展示自动脱敏，防止 API Key 泄露

---

## [v1.0.0] - 2026-05-09

### ✨ 新增功能

- **微信公众号完整接入**
  - `GET /wechat/callback` - 微信服务器接入验证
  - `POST /wechat/callback` - 处理微信回调消息
  - 支持所有消息类型：文本、图片、语音、视频、短视频、位置、链接
  - 支持事件处理：关注、取消关注、菜单点击等

- **用户体系一期**
  - 关注公众号时自动创建用户账号
  - 微信账号与用户关联（OpenID / UnionID）
  - 支持多微信账号关联同一用户

- **LLM 对话集成**
  - 统一的 LLM 客户端抽象层
  - 支持 OpenAI 兼容 API
  - 所有消息类型统一调用大模型回复

- **管理 API**
  - `GET /api/v1/health` - 健康检查
  - `GET /api/v1/metrics` - 系统指标
  - `GET /api/v1/config` - 查看配置
  - `PUT /api/v1/config` - 更新配置

- **基础设施**
  - 数据库抽象层（SQLite / PostgreSQL 支持）
  - Zap 高性能结构化日志，支持文件轮转
  - Viper 配置管理，支持环境变量覆盖
  - Gin Web 框架，中间件完整

### 🗄️ 数据库表

- `users` - 用户表
- `wechat_accounts` - 微信账号关联表

---

## 版本说明

- **MAJOR 版本**：不兼容的 API 变更
- **MINOR 版本**：向后兼容的功能性新增
- **PATCH 版本**：向后兼容的问题修正

本项目采用 [语义化版本号 (Semantic Versioning)](https://semver.org/lang/zh-CN/)。
