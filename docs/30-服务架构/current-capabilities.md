# 当前项目能力说明

本文档按项目目录结构，记录每个模块当前已实现的能力和入口位置，便于后续开发定位。

---

## cmd/server/

### main.go
**入口函数**: `main()`

**已实现能力**:
- 解析命令行参数 `-config` 指定配置文件
- 调用 `config.Load()` 加载配置
- 调用 `logger.Init()` 初始化日志
- 根据环境设置 Gin 模式
- 调用 `api.SetupRouter()` 创建路由
- 启动 HTTP 服务器，支持优雅关闭
- 监听 SIGINT/SIGTERM 信号，正确关闭服务器

---

## internal/api/

### routes.go
**入口函数**: `SetupRouter()`

**已实现能力**:
- 创建 Gin 引擎
- 注册全局中间件（Logger, Recovery, CORS）
- 初始化依赖注入链：DB → Repository → Service → Handler
- 注册路由:
  - `GET /ping` → 服务探活
  - `GET /wechat/callback` → `wechat.Handler.Verify`
  - `POST /wechat/callback` → `wechat.Handler.HandleMessage`
  - `GET /api/v1/health` → 健康检查
  - `GET /api/v1/metrics` → 系统指标
  - `GET /api/v1/config` → 配置管理
  - `PUT /api/v1/config` → 更新配置

---

## internal/api/wechat/

### handler.go
**入口函数**:
- `Verify(c *gin.Context)` - 微信服务器接入验证
- `HandleMessage(c *gin.Context)` - 处理微信回调消息

**已实现能力**:
- ✅ 微信签名验证（符合微信官方文档）
- ✅ GET 请求接入验证，成功返回 echostr
- ✅ 解析微信 XML 格式消息，支持所有消息类型
- ✅ LLM 智能对话集成：文本/图片/语音/视频等消息自动调用大模型回复
- ✅ **用户自定义 LLM 配置命令**:
  - `#模型设置` → 显示配置引导菜单
  - `#设置Key sk-xxx` → 设置用户 API Key（AES-256-GCM 加密存储）
  - `#设置地址 https://xxx` → 设置自定义 API 端点
  - `#我的配置` → 查看当前配置（Key 脱敏显示）
  - `#重置模型` → 清除自定义配置，恢复系统默认
- ✅ 支持所有 OpenAI API 兼容的大模型服务（Azure、One API、私有部署等）
- ✅ 用户配置与系统配置隔离，每个用户独立使用自己的配额
- ✅ 配置调用失败时返回友好错误提示
- ✅ **上下文记忆集成**：自动加载最近 10 轮历史消息构建对话上下文
- ✅ **消息永久落库**：所有用户消息和 AI 回复都持久化存储
- ✅ **消息自动去重**：基于 MsgID 唯一索引，避免微信重试导致重复回复
- ✅ **失败降级策略**：保存消息失败不影响对话流程，仅记录告警日志

**关注事件处理**:
- ✅ subscribe（关注） → 自动创建用户账户
- ✅ unsubscribe（取消关注） → 记录但不删除数据
- ✅ 其他事件类型

---

## internal/api/admin/

### handler.go
**入口函数**:
- `HealthCheck(c *gin.Context)`
- `Metrics(c *gin.Context)`
- `GetConfig(c *gin.Context)`
- `UpdateConfig(c *gin.Context)`

**已实现能力**:
- ✅ `GET /api/v1/health` 健康检查
- ✅ `GET /api/v1/metrics` 系统指标（数据库连接池统计）
- ✅ `GET /api/v1/config` 获取当前配置
- ✅ `PUT /api/v1/config` 动态更新配置

---

## internal/client/llm/

### client.go
**入口函数**: `NewClient(cfg config.LLMConfig) (LLMClient, error)`

**已实现能力**:
- ✅ 多 LLM 提供商抽象层
- ✅ 支持多种 OpenAI 兼容的 API 提供商
- ✅ 统一的 `ChatCompletion()` 接口
- ✅ 可扩展的 Provider 架构，便于新增支持

**支持的 Provider**:
- OpenAI 官方 API
- 兼容 OpenAI 格式的第三方服务

---

## internal/domain/user/

### user.go
**实体**: `User`, `WechatAccount`

**已实现能力**:
- ✅ 用户核心实体定义
- ✅ 微信账号关联（OpenID / UnionID）
- ✅ 多账号关联同一用户
- ✅ 用户状态管理（正常/禁用/已删除）

### llm_config.go
**实体**: `LLMConfig`

**已实现能力**:
- ✅ 用户自定义 LLM 配置实体
- ✅ API Key 加密存储（AES-256-GCM）
- ✅ 自定义 BaseURL、Model 支持
- ✅ 配置启用/禁用状态管理
- ✅ user_id 唯一索引，每个用户只能有一份配置

### message.go
**实体**: `Message`

**已实现能力**:
- ✅ 对话消息实体定义
- ✅ 支持用户消息和 AI 回复两种角色
- ✅ MsgID 字段用于微信消息去重
- ✅ 关联 UserID，按用户隔离消息历史
- ✅ 支持多轮对话上下文追踪

---

## internal/repository/user/

### user_repo.go
**接口**: `UserRepository`

**已实现能力**:
- ✅ `Create(user *domain.User) error`
- ✅ `GetByID(id int64) (*domain.User, error)`
- ✅ `Update(user *domain.User) error`

### wechat_account_repo.go
**接口**: `WechatAccountRepository`

**已实现能力**:
- ✅ `Create(account *domain.WechatAccount) error`
- ✅ `GetByOpenID(openID string) (*domain.WechatAccount, error)`
- ✅ `GetByUnionID(unionID string) (*domain.WechatAccount, error)`
- ✅ `GetByUserID(userID int64) ([]*domain.WechatAccount, error)`

### llm_config_repo.go
**接口**: `LLMConfigRepository`

**已实现能力**:
- ✅ `Create(config *domain.LLMConfig) error`
- ✅ `GetByUserID(userID int64) (*domain.LLMConfig, error)`
- ✅ `Update(config *domain.LLMConfig) error`
- ✅ `Delete(userID int64) error`

---

## internal/repository/chat/

### message_repo.go
**接口**: `MessageRepository`

**已实现能力**:
- ✅ `Create(msg *domain.Message) error` - 创建消息（带唯一索引去重）
- ✅ `GetByMsgID(msgID string) (*domain.Message, error)` - 按 MsgID 查询（去重校验）
- ✅ `GetRecentByUser(userID int64, limit int) ([]*domain.Message, error)` - 获取用户最近 N 条消息
- ✅ `CountByUser(userID int64) (int64, error)` - 统计用户消息数
- ✅ MsgID 唯一索引，自动去重微信重试消息

---

## internal/service/user/

### user_service.go
**接口**: `UserService`

**已实现能力**:
- ✅ `GetOrCreateByOpenID(openID string) (*domain.User, bool, error)`
- ✅ 首次关注自动创建用户账户
- ✅ 自动关联微信账号与用户
- ✅ 幂等设计，重复调用安全

### llm_config_service.go
**接口**: `LLMConfigService`

**已实现能力**:
- ✅ `SetAPIKey(userID int64, apiKey string) error` - 设置 API Key（自动加密）
- ✅ `SetBaseURL(userID int64, baseURL string) error` - 设置 API 地址
- ✅ `SetModel(userID int64, model string) error` - 设置默认模型
- ✅ `GetConfigForUser(userID int64) (apiKey, baseURL, model string, hasCustom bool, err error)` - 获取解密后的配置（LLM 调用时使用）
- ✅ `GetConfigView(userID int64) (*LLMConfigView, error)` - 获取配置视图（API Key 脱敏，用于前端展示）
- ✅ `ClearConfig(userID int64) error` - 清除用户自定义配置

**校验规则**:
- API Key 必须以 `sk-` 开头
- 长度校验（30-200 字符）
- URL 必须以 `http://` 或 `https://` 开头

---

## internal/service/chat/

### message_service.go
**接口**: `MessageService`

**已实现能力**:
- ✅ `SaveUserMessage(userID int64, msgID, content string) error` - 保存用户消息（自动去重）
- ✅ `SaveAssistantMessage(userID int64, content string) error` - 保存 AI 回复
- ✅ `GetContextMessages(userID int64) ([]*domain.Message, error)` - 获取上下文消息（最近 10 轮）
- ✅ `BuildPromptWithContext(userID int64, currentContent string) string` - 构建带上下文的 Prompt
- ✅ **自动去重**：MsgID 已存在时静默返回，不重复处理
- ✅ **失败降级**：保存消息失败仅记录日志，不中断对话流程
- ✅ **上下文窗口管理**：默认保留最近 10 轮对话，超出自动截断

---

## internal/pkg/crypto/

### aes.go
**入口函数**:
- `Encrypt(plaintext string, key ...[]byte) (string, error)`
- `Decrypt(ciphertext string, key ...[]byte) (string, error)`

**已实现能力**:
- ✅ AES-256-GCM 认证加密
- ✅ 随机 nonce（每次加密结果不同）
- ✅ base64 编码输出
- ✅ 支持环境变量 `LLM_CONFIG_ENCRYPT_KEY` 指定加密密钥
- ✅ 开发环境内置默认密钥（便于测试）

---

## internal/db/

### database.go
**入口函数**: `InitDB(cfg *config.DatabaseConfig, opts ...Option) (*Database, error)`

**已实现能力**:
- ✅ 多驱动支持（SQLite / PostgreSQL / MySQL 预留）
- ✅ 连接池配置（最大连接、最大空闲、生命周期）
- ✅ 健康检查
- ✅ 事务支持
- ✅ 优雅关闭
- ✅ **自动迁移表结构**:
  - `users` - 用户表
  - `wechat_accounts` - 微信账号关联表
  - `user_llm_configs` - 用户 LLM 配置表

---

## internal/middleware/

### middleware.go
**已实现**:
- CORS 中间件
- Logger 中间件
- Recovery 中间件

---

## pkg/config/

### config.go
**入口函数**: `Load(configPath string) (*Config, error)`

**已实现能力**:
- ✅ 基于 Viper 配置管理
- ✅ 支持 YAML 配置文件
- ✅ 自动读取环境变量（前缀 `WECHAT_BOT_`）
- ✅ 如果未指定配置文件，自动尝试 `configs/config.yaml`，不存在则使用 `configs/config.example.yaml`
- ✅ 配置结构体定义完整：
  - `AppConfig` - 应用基本配置
  - `WechatConfig` - 微信公众号配置
  - `LLMConfig` - 大模型配置（多提供商支持）
  - `DatabaseConfig` - 数据库配置
  - `MemoryConfig` - 记忆系统配置（预留）
  - `RedisConfig` - Redis 配置（预留）
  - `LoggerConfig` - 日志配置

---

## pkg/logger/

### logger.go
**入口函数**:
- `Init(cfg config.LoggerConfig)` - 初始化日志
- `Info(args...)`, `InfoWithFields(msg string, fields ...zap.Field)` - 信息日志
- `Warn(args...)`, `WarnWithFields(...)` - 警告日志
- `Error(args...)`, `ErrorWithFields(...)` - 错误日志
- `Fatal(args...)`, `FatalWithFields(...)` - 致命错误日志
- `Debug(args...)`, `DebugWithFields(...)` - 调试日志

**已实现能力**:
- ✅ 基于 Zap 高性能结构化日志
- ✅ 支持 Lumberjack 日志轮转
- ✅ 支持控制台输出和文件输出
- ✅ 支持配置日志级别
- ✅ 支持 JSON/Console 格式

---

## 配置文件

### configs/config.example.yaml
**说明**: 配置文件模板，包含所有配置项注释

### configs/config.prod.yaml
**说明**: 生产环境配置示例

---

## 整体当前能力总结

```
用户微信公众号 → 微信服务器 → 我们的服务
    ↓
签名验证通过 → 解析 XML 消息 → 消息去重校验
    ↓
获取/创建用户 → 保存用户消息到数据库
    ↓
    ├───────────────────────────────────────────┐
    │ 配置命令？（#模型设置/#设置Key/...）          │
    │      ↓                                      │
    │  处理配置命令 → 加密存储 / 脱敏展示           │
    │      ↓                                      │
    │  返回操作结果（成功/失败提示）                 │
    └───────────────────────────────────────────┘
    ↓ 否，普通对话
加载最近 10 轮历史消息 → 构建对话上下文 Prompt
    ↓
检查用户是否有自定义 LLM 配置
    ├─ 有自定义配置 → 使用用户的 API Key/地址 调用 LLM
    │
    └─ 无自定义配置 → 使用系统默认 LLM 配置
    ↓
LLM 生成回复 → 保存 AI 回复到数据库
    ↓
转换为微信 XML 格式 → 返回给用户
```

**当前项目可交付状态**：
- ✅ 微信公众号完整接入（消息接收/回复/事件处理）
- ✅ 用户体系（关注自动创建账号，微信账号关联）
- ✅ 大模型对话能力（支持 OpenAI 兼容的所有提供商）
- ✅ **用户自定义 LLM 配置**（v1.1 新功能）
  - 每个用户可独立设置自己的 API Key、API 地址
  - 配置加密存储，安全可靠
  - 对话时优先使用用户自己的配置，配额独立
  - 可随时重置回系统默认
- ✅ **对话上下文记忆**（v1.2 新功能）
  - 全局上下文记忆：自动保留最近 10 轮对话历史
  - 所有对话消息永久落库，便于追溯和分析
  - 微信消息自动去重：基于 MsgID 唯一索引，避免重试导致重复回复
  - 失败降级策略：消息保存失败仅记录日志，不影响对话流程
  - 上下文自动构建：历史消息自动格式化为 Prompt 传入 LLM
- ✅ 完整的管理 API（健康检查、配置管理）

**下一步可迭代方向**：
- v1.3: 向量检索增强记忆
- v2.0: H5 配置页面、多模型切换
