# 20-产品PRD

本目录存放产品需求文档，按状态和模块分类组织。

## 目录结构

```
20-产品PRD/
├── backlog/          # 待排期的需求池
├── in_progress/      # 开发中的需求
├── completed/        # 已上线的需求归档
└── README.md
```

## 需求状态流转

```
backlog → in_progress → completed
```

## PRD 模板要求

每个 PRD 文档应包含：
1. 需求背景与目标
2. 用户故事 / 功能描述
3. 交互流程
4. 验收标准
5. 非功能需求
6. 埋点需求

## 已完成 PRD 列表

| 文档 | 版本 | 说明 |
|------|------|------|
| 用户自定义LLM配置PRD-v1.0.md | v1.0 | 微信命令式配置自定义 LLM，AES 加密存储 |
| 用户体系PRD-v1.0.md | v1.0 | 关注自动创建用户，OpenID/UnionID 关联 |
| llm-client-integration.md | v1.0 | OpenAI 兼容 LLM 客户端集成 |
| wechat-llm-integration.md | v1.0 | 微信消息与 LLM 对话集成 |
| wechat-message-fixed-reply.md | v1.0 | 微信消息固定回复功能 |

