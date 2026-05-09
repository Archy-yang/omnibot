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
- 注册全局中间件（Logger, Recovery）
- 注册路由:
  - `GET /ping` → `admin.Handler.HealthCheck`
  - `GET /wechat/callback` → `wechat.Handler.Verify`
  - `POST /wechat/callback` → `wechat.Handler.HandleMessage`

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
- ✅ 所有消息类型（文本/图片/语音/视频/位置/链接/事件）统一返回固定回复
- ✅ 固定回复内容: **"客服在开发中，敬请期待"**
- ✅ 回复格式符合微信要求的 XML 格式
- ✅ ToUserName 和 FromUserName 正确交换

**配置**: 从 `Config` 获取 `Token` 用于签名验证

---

## internal/api/admin/

### handler.go
**入口函数**:
- `HealthCheck(c *gin.Context)`

**已实现能力**:
- ✅ `GET /ping` 返回 `pong`，用于服务探活

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
  - `WechatConfig` - 微信公众号配置（AppID/AppSecret/Token/EncodingAESKey/CallbackURL）
  - `LLMConfig` - 大模型配置（多提供商支持）
  - `MemoryConfig` - 记忆系统配置
  - `RedisConfig` - Redis 配置
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
签名验证通过 → 解析消息 → 返回固定回复 "客服在开发中，敬请期待"
    ↓
用户看到回复
```

当前项目可交付状态：**微信接入完成，可正常接收消息并回复占位文本**。等待核心业务功能（大模型对话、记忆系统）开发。
