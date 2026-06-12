# 变更日志

本文件记录项目所有重要版本的变更内容。

---

## [v1.3.0] - 2026-06-13

### ✨ 新增功能

- **单条记忆删除与编辑**
  - 微信命令 `#删除记忆 N` 按序号删除单条记忆
  - Web API `DELETE /api/v1/memories/:id` 和 `PUT /api/v1/memories/:id`
  - Web 端记忆管理 UI：每条记忆支持删除按钮（确认弹窗）和内联编辑
  - 输入校验：空内容提示、200 字上限

- **SSE 流式对话输出**
  - 新增 `POST /api/v1/chat/messages/stream` SSE 端点
  - LLM Client 接口新增 `StreamChatCompletion`，OpenAI Provider 实现
  - 前端 `sendMessageStream` 使用 fetch + ReadableStream 逐 chunk 渲染
  - 打字机效果，用户无需等待完整响应
  - 同步端点保留，提供降级能力

### 🔧 架构改进

- **Chat 层窄接口 `LongTermMemoryProvider`**：chat 层只依赖 `GetRecentForContext`，不再依赖完整 `MemoryService`
- **Graphify 生成物清理**：`.gitignore` 新增 `graphify-out/`，已跟踪文件从索引移除
- **CLAUDE.md 路径修复**：演进路线图文档路径更新为重组后位置

### 📚 文档同步

- PRD、设计文档、系统架构总览、消息服务文档、微信通道文档全面更新
- 新增 `docs/90-迭代记录/archived/v1.3-长期记忆增强-SSE流式.md` 功能总结

### 🧪 测试

- 新增 29 个测试（21 个记忆删除/编辑 + 8 个 SSE 流式）
- `go test ./...` 全部通过
- `npm run build` 构建成功

---

## [v1.2.0] - 2026-05-12

### ✨ 新增功能

- **对话上下文记忆**
  - 机器人自动记住每个用户最近 10 轮对话内容
  - 用户可以使用「这个」「刚才的」「改成」等指代性词语进行连贯交流
  - 所有对话消息永久落库保存（用户消息 + AI 回复）
  - 调用 LLM 时自动拼接最近 10 轮历史消息作为上下文
  - 每次调用前重新插入 system prompt，保证人设一致性

- **消息去重机制**
  - 通过微信 MsgID 唯一索引去重
  - 防止微信服务器重试导致的重复消息落库
  - 重复消息记录日志但不影响业务流程

- **MessageService 对话服务层**
  - `BuildContextMessages(ctx, userID, currentContent)` - 构建上下文消息列表
  - `SaveUserMessage(ctx, userID, content, msgID)` - 保存用户消息（带去重）
  - `SaveAssistantMessage(ctx, userID, content)` - 保存 AI 回复

- **MessageRepository 对话仓储层**
  - `Create(msg)` - 创建消息
  - `GetRecentByUserID(userID, limit)` - 查询用户最近 N 条消息
  - `ExistsByMsgID(msgID)` - 检查 MsgID 是否已存在

### 🔧 架构改进

- **滑动窗口上下文**：永久存储所有消息，调用 LLM 时只取最近 10 轮（20 条）
- **降级策略**：数据库查询失败时自动降级为单轮对话模式，对用户无感知
- **消息过滤**：只有文本消息进入上下文，图片/语音/视频等非文本消息正常回复但不入库
- **失败兜底回复入上下文**：LLM 调用失败的提示也保存，避免上下文断裂

### 📦 数据变更

- 新增 `messages` 表存储所有对话消息
- 包含字段：`id`、`user_id`、`role`、`content`、`msg_id`、`created_at`
- `msg_id` 唯一索引（去重）
- `user_id` 普通索引（按用户查询）
- `role` 普通索引（user/assistant/system/tool）

### 🔐 安全增强

- 日志中不输出完整对话内容，只输出消息 ID、长度、角色等元数据

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
