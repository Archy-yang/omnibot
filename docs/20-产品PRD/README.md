# 20-产品PRD

本目录存放产品需求文档，按状态和模块分类组织。

## 目录结构

```
20-产品PRD/
├── backlog/          # 待排期的需求池(当前为空)
├── in_progress/      # 开发中的需求(当前为空——快速迭代期的需求以技术方案文档先行)
├── completed/        # 已完成开发的需求归档(验收状态见各文档头标注)
└── README.md
```

## 需求状态流转

```
backlog → in_progress → completed
```

> 注：v1.11 起进入未版本化快速迭代期，多数能力不再单独立 PRD，由
> `30-服务架构/01-高层设计/` 对应技术方案承载需求与设计（如记忆系统见 12 号、
> 后台 Agent 框架见 08 号）。

## PRD 模板要求

每个 PRD 文档应包含：
1. 需求背景与目标
2. 用户故事 / 功能描述
3. 交互流程
4. 验收标准
5. 非功能需求
6. 埋点需求

## 已完成 PRD 列表

### v2.x 系列（已开发完成，验收状态见文档头标注）

| 文档 | 说明 |
|------|------|
| v2.1-邮箱密码登录注册PRD.md | JWT 鉴权 + 单一身份模型 |
| v2.2-飞书账号绑定PRD.md | 飞书身份绑定到统一用户 |
| v2.3-微信账号绑定PRD.md | 微信身份绑定,全平台单一身份成立 |
| 高级记忆系统PRD-v1.0.md | M1~M8 记忆演进的需求源（实现偏差见文档头补记,现行方案以 12-记忆系统技术方案为准） |
| 后台Agent任务框架PRD-v1.0.md | 派活/汇报/任务中心（实现以 08 号方案与 16 号路线图为准） |

### v1.x 系列

| 文档 | 版本 | 说明 |
|------|------|------|
| 用户自定义LLM配置PRD-v1.0.md | v1.0 | 微信命令式配置自定义 LLM，AES 加密存储 |
| 用户体系PRD-v1.0.md | v1.0 | 关注自动创建用户，OpenID/UnionID 关联 |
| llm-client-integration.md | v1.0 | OpenAI 兼容 LLM 客户端集成 |
| wechat-llm-integration.md | v1.0 | 微信消息与 LLM 对话集成 |
| wechat-message-fixed-reply.md | v1.0 | 微信消息固定回复功能 |
| openai-api-compatible-client.md | v1.0 | OpenAI 兼容协议客户端 |
| v1.4-Web对话页面PRD.md | v1.4 | Web 端对话界面 |
| v1.4.1-OpenAI兼容服务商预设配置PRD.md | v1.4.1 | 服务商预设 |
| v1.5-Agent基本能力PRD.md | v1.5 | Agent 工具调用 |
| v1.5.1-Agent模式可切换PRD.md | v1.5.1 | Agent 模式开关 |
| v1.5.2-Agent真流式与默认开启PRD.md | v1.5.2 | 真流式输出 |
| v1.6-飞书机器人接入PRD.md | v1.6 | 飞书长连接接入 |
| Web端长期记忆管理PRD-v1.0.md | v1.0 | 记忆抽屉管理界面 |
| 插件系统PRD-v1.0.md | v1.0 | Tool/MCP 插件系统（实现见 13 号方案） |
| 对话上下文记忆PRD-v1.2.md | v1.2 | 对话上下文 |
| 长期记忆MVP-PRD-v1.3.md | v1.3 | 长期记忆最小可用版 |
| current-development.md | — | 最早期的进度备忘(非 PRD,仅留档) |
