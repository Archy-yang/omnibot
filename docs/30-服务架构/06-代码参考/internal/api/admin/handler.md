# 架构说明：internal/api/admin/handler.go

## 模块职责
提供管理接口，包括:
- 服务探活 (health check)

## 核心结构体
```go
type Handler struct {
    // 暂不需要额外配置
}
```

## 公开方法

| 方法 | 职责 |
|------|------|
| `NewHandler() *Handler` | 创建处理器 |
| `HealthCheck(c *gin.Context)` | 健康检查/探活 |

## 已实现能力
- ✅ `GET /ping` 返回 `pong`
- ✅ 用于 Kubernetes/负载均衡健康检查

## 使用场景
- 服务启动探测
- 负载均衡健康检查
- CI/CD 可用性验证
