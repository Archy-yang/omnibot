# 架构说明：cmd/server/main.go

## 模块职责
服务启动入口，负责：
1. 解析命令行参数
2. 加载配置
3. 初始化日志
4. 设置 Gin 运行模式
5. 创建路由
6. 启动 HTTP 服务器
7. 监听信号，优雅关闭

## 入口函数
`main()`

## 流程
```
1. 解析命令行参数 → -config 指定配置文件路径
   ↓
2. config.Load() → 加载配置到 Config 结构体
   ↓
3. logger.Init() → 初始化日志系统
   ↓
4. 根据配置环境设置 Gin 模式 (debug/release)
   ↓
5. api.SetupRouter() → 创建 Gin 引擎并注册路由
   ↓
6. 创建 http.Server，指定监听地址
   ↓
7. 启动服务器 (goroutine 中)
   ↓
8. 等待 SIGINT/SIGTERM 信号
   ↓
9. 上下文超时关闭服务器
   ↓
10. 退出
```

## 已实现能力
- ✅ 命令行参数指定配置文件
- ✅ 优雅关闭支持
- ✅ 环境判断设置 Gin 模式

## 依赖
- `pkg/config` - 配置加载
- `pkg/logger` - 日志初始化
- `internal/api` - 路由设置
