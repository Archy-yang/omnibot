# 架构说明：pkg/logger/logger.go

## 模块职责
全局日志工具，基于 Zap 提供高性能结构化日志。

## 入口函数
`Init(cfg config.LoggerConfig)` - 初始化日志系统，必须在使用日志前调用。

## 导出函数

| 函数 | 说明 |
|------|------|
| `Debug(args ...interface{})` | Debug 级别日志（Sugar 风格） |
| `DebugWithFields(msg string, fields ...zap.Field)` | Debug 带字段 |
| `Info(args ...interface{})` | Info 级别 |
| `InfoWithFields(msg string, fields ...zap.Field)` | Info 带字段 |
| `Warn(args ...interface{})` | Warn 级别 |
| `WarnWithFields(msg string, fields ...zap.Field)` | Warn 带字段 |
| `Error(args ...interface{})` | Error 级别 |
| `ErrorWithFields(msg string, fields ...zap.Field)` | | Error 带字段 |
| `Fatal(args ...interface{})` | Fatal 级别（退出程序） |
| `FatalWithFields(msg string, fields ...zap.Field)` | Fatal 带字段 |

## 特性
- **高性能**: 基于 Zap 结构化日志
- **日志轮转**: 使用 Lumberjack 支持按文件大小轮转，保留 7 个备份，保留 28 天
- **输出位置**: 支持控制台输出 (stdout) 或文件输出
- **日志级别**: 可通过配置控制输出级别
- **调用者信息**: 自动记录文件名和行号

## 配置
通过 `config.LoggerConfig`:
| 字段 | 说明 |
|------|------|
| Level | 日志级别 (debug/info/warn/error) |
| Format | 输出格式 (json/console) |
| Output | 输出位置 (stdout/文件路径) |

## 已实现能力
- ✅ 高性能结构化日志
- ✅ 自动日志轮转
- ✅ 支持控制台/文件输出
- ✅ 分级日志 API
- ✅ 带字段日志支持

## 依赖
- `go.uber.org/zap`
- `gopkg.in/natefinch/lumberjack.v2`
