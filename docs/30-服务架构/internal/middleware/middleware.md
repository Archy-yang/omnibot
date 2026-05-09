# 架构说明：internal/middleware/middleware.go

## 模块职责
提供 Gin 中间件:
- CORS 跨域处理
- Logger 请求日志
- Recovery panic 恢复

## 已实现中间件

### CORS()
处理跨域请求，允许:
- 任意来源
- 任意方法
- 任意头

### Logger()
记录请求信息:
- 方法
- 路径
- 状态码
- 响应时间
- 客户端 IP

### Recovery()
捕获 panic，返回 500 错误，避免服务崩溃。

## 已实现能力
- ✅ CORS 跨域支持
- ✅ 请求日志记录
- ✅ panic 恢复
