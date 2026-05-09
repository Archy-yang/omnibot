# 架构说明：internal/api/routes.go

## 模块职责
创建 Gin 引擎，注册所有路由和中间件。

## 入口函数
`SetupRouter(cfg *config.Config) *gin.Engine`

## 已注册路由

| 方法 | 路径 | 处理器 | 说明 |
|------|------|------|------|
| GET | `/ping` | admin.Handler.HealthCheck | 服务探活 |
| GET | `/wechat/callback` | wechat.Handler.Verify | 微信服务器接入验证 |
| POST | `/wechat/callback` | wechat.Handler.HandleMessage | 接收微信消息 |

## 已注册全局中间件
- `middleware.Logger()` - 日志记录
- `middleware.Recovery()` - panic 恢复
- `middleware.CORS()` - CORS 支持

## 已实现能力
- ✅ 创建 Gin 引擎
- ✅ 注册全局中间件
- ✅ 按模块分组注册路由
- ✅ 返回配置好的 *gin.Engine

## 依赖
- `internal/api/admin` - 管理接口处理器
- `internal/api/wechat` - 微信处理器
- `internal/middleware` - 中间件
- `pkg/config` - 配置
