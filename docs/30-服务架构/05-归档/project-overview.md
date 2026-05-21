# 项目概览

## Project Description

微信公众号智能对话系统，集成了微信公众号、大模型服务和记忆系统的个性化智能对话服务。

**核心目标**: 用户在微信公众号发送消息，系统调用大模型生成回复，并基于对话内容抽取用户记忆，提供个性化对话服务。

技术栈：Go 1.24 + Gin 框架 + Viper 配置管理 + Zap 日志

## Overall Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   微信服务器                                │
└─────────────────────┬─────────────────────────────────────┘
                      │
                      ▼
          ┌──────────────────────────┐
          │   /wechat/callback       │
          │   (签名验证 + 消息处理)  │
          └─────────────────┬────────┘
                            │
                            ▼
                   ┌─────────────────┐
                   │   对话服务模块     │
                   │   (记忆检索 + 上下文  │
                   │    构建 + LLM调用)  │
                   └─────────┬───────────┘
                            │
                            ▼
                   ┌─────────────────┐
                   │   记忆服务模块     │
                   │ (抽取 + 存储 + 检索)│
                   └─────────────────┘
                            │
                            ▼
                 返回回复给微信服务器
                      ↓
                  用户收到回复
```

## Code Directory Structure

| 目录 | 职责 |
|------|------|
| `cmd/server/` | 服务入口，启动 HTTP 服务器 |
| `internal/api/` | API 路由和处理器 |
| `internal/api/wechat/` | 微信回调接口处理 |
| `internal/api/admin/` | 管理接口 |
| `internal/middleware/` | Gin 中间件 |
| `internal/client/llm/` | 大模型客户端 | *待实现*
| `internal/service/chat/` | 对话服务 | *待实现*
| `internal/service/memory/` | 记忆服务 | *待实现*
| `pkg/config/` | 配置加载（基于 Viper） |
| `pkg/logger/` | 日志管理（基于 Zap + Lumberjack 轮转） |
| `configs/` | 配置文件（example + prod） |

## Entry Flow

1. `cmd/server/main.go` 解析命令行参数，加载配置
2. 初始化日志，设置 Gin 模式
3. `internal/api.SetupRouter()` 注册路由
4. 启动 HTTP 服务器，优雅关闭支持

## Current Implemented Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/ping` | 服务探活 |
| GET | `/wechat/callback` | 微信服务器接入验证 |
| POST | `/wechat/callback` | 接收微信消息 |

## Current Capability Summary

当前状态：**微信接入完成，可正常接收消息并返回占位回复**

所有用户消息统一返回：`客服在开发中，敬请期待`

下一步：大模型客户端接入 → 对话服务 → 记忆服务
