# OmniBot — 全平台智能助手

OmniBot 是一个**有长期记忆的、跨入口的私人 AI 助手**。它不是传统的"聊天工具",
而是把同一个 AI 助手接入到 Web、微信公众号、飞书等多个入口,所有入口共享同一份
身份、记忆和对话历史——无论你从哪里发消息,面对的都是同一个"记得你"的助手。

## 核心理念

### 单一长期对话模型

OmniBot 的产品基线是**每个用户只有一个永久对话**:

- 不引入"会话"概念,不提供"新建对话""会话列表""切换会话"
- 所有消息按 `user_id` 落库,跨入口、跨时间连续
- 类比对象不是 ChatGPT,而是"微信里一个始终在线、记得你的好友"

详见 [`10-宪章/单一长期对话模型.md`](10-宪章/单一长期对话模型.md)。

### 入口无关 + 跨入口身份合一

通过 `UserChannels` 模型(channel_type + channel_user_id → user_id),不同入口的
身份解析统一收敛到同一个 `user_id`。在 Web 端配置的 LLM、记下的长期记忆,飞书端
自动共享。

## 核心特性

### 1. 多入口接入
- **Web 网页**:浏览器端聊天 + 设置 + 长期记忆管理
- **微信公众号**:被动回复 + 文本命令(XML 协议已解耦)
- **飞书机器人**:长连接(WebSocket)IM 单聊 + 跨入口记忆共享 + markdown 卡片渲染

### 2. Agent 能力(ReAct 循环)
LLM 自主判断是否调用工具,多步推理直到生成最终回复。内置工具:

| 工具 | 功能 |
|------|------|
| `get_current_time` | 获取当前时间 |
| `calculator` | 数学表达式计算 |
| `search_memories` | 搜索用户长期记忆 |
| `search_history` | 搜索对话历史 |
| `rss_reader` | 读取 RSS 源 |

Web 端通过 SSE 实时推送 agent 思考过程(工具调用过程可折叠展开);飞书等 IM 入口
走同步聚合路径,二者运行链路记录(`agent_steps`)对齐。

### 3. 长期记忆
- 记忆与对话历史是两套独立数据,互不影响(清空对话不影响记忆,反之亦然)
- 每次对话自动注入最近长期记忆到上下文(对用户透明)
- Web 端记忆管理:查看 / 新增 / 编辑 / 删除 / 清空,单条上限 200 字
- 安全校验:提醒不要保存密码、API Key、身份证号等敏感信息

### 4. 用户自定义 LLM 配置
- 用户可在 Web 设置面板配置自己的服务商 / 模型 / API Key,加密存储到
  `user_llm_configs` 表,命中时覆盖系统默认配置
- 支持 OpenAI 兼容模式(OpenAI / DeepSeek / 百度千帆 / 字节火山 / 阿里千问等)

## 技术栈

| 层 | 选型 |
|----|------|
| 后端 | Go + Gin |
| ORM | GORM |
| 数据库 | SQLite(开发)/ PostgreSQL(生产),双驱动 |
| 日志 | Zap |
| 架构 | DDD 分层(API → Service → Repository → Domain) |
| 前端 | Vue 3 (`<script setup>` + TypeScript) + Vite + Pinia |
| LLM 协议 | OpenAI Compatible |

## 系统架构

分层约束(详见 [`10-宪章/代码风格规范.md`](10-宪章/代码风格规范.md)):

- API 层只依赖 Service 层
- Service 层只依赖 Repository 层和 Domain 层
- Domain 层不依赖任何其他层
- 所有跨层调用通过接口

架构演进路线详见 [`30-服务架构/01-高层设计/02-演进路线图-v3.0.md`](30-服务架构/01-高层设计/02-演进路线图-v3.0.md)。

## 快速开始

### 环境要求
- Go 1.21+
- PostgreSQL 14+(或用 SQLite 内存库快速起步)

### 配置

```bash
# 复制配置模板,填入本地凭证(此文件已在 .gitignore,不会进 git)
cp configs/config.example.yaml configs/config.yaml
```

`configs/config.yaml` 关键段:
- `database`:Postgres DSN 或 SQLite
- `feishu`:`app_id` / `app_secret` / `enabled`(接入飞书时填)
- `wechat`:微信公众号凭证(接入微信时填)
- `llm`:系统默认 LLM(可留占位,用户在 Web 设置面板自配)

> LLM API Key 等真实凭证建议全程走 Web 设置面板配置(加密落库),
> 不必写进 config.yaml。

### 启动

```bash
# 开发模式:同时启动后端(:8080)和前端(:5173)
./start.sh dev

# 或分别启动
go run cmd/server/main.go -config configs/config.yaml
```

### 构建

```bash
# 完整构建:打包前端 + 编译后端(前端通过 Go Embed 嵌入二进制)
./start.sh build

# 运行已编译二进制
./start.sh start
```

### 测试

```bash
go test ./...
```

## 主要 API 端点

| 端点 | 说明 |
|------|------|
| `GET/POST /wechat/callback` | 微信服务器回调入口 |
| `GET /api/v1/health` | 健康检查 |
| `GET/POST /api/v1/chat/messages` | 获取历史 / 发送消息 |
| `POST /api/v1/chat/messages/agent/stream` | Agent 流式对话(SSE) |
| `GET/POST/PUT/DELETE /api/v1/memories` | 长期记忆 CRUD |
| `GET/PUT/DELETE /api/v1/user/llm-config` | 用户 LLM 配置 |
| `GET /api/v1/user/llm-providers` | 服务商预设列表 |

飞书机器人通过长连接(WebSocket)接收消息,无需公网回调地址。

## 文档目录结构

```
docs/
├── 10-宪章/             # 开发规范、产品愿景、核心基线(最高优先级)
├── 20-产品PRD/          # 产品需求文档,按状态分类(backlog/in_progress/completed)
├── 30-服务架构/         # 技术架构、系统设计、模块文档、演进路线图
├── 40-踩坑记录/         # 技术问题复盘与解决方案
├── 50-测试/             # 测试计划、测试报告
│   ├── test-plans/      # 测试计划
│   └── test-reports/    # 测试报告
├── 60-设计/             # 前端页面设计原型与交互逻辑
└── 90-迭代记录/         # 版本迭代、变更日志、发布记录
```

文档体系优先级:**10-宪章 → 30-服务架构 → 20-产品PRD → 具体迭代任务**。

## 开发约定

- 修改前先出方案,确认后再实施
- 严格 TDD:先写测试,测试失败再写实现
- 最小改动原则,不夹带无关重构
- 功能分支开发 + `--no-ff merge`,不直接在 master 提交
- 用户敏感数据(API Key / 密码)加密存储,日志不输出敏感信息和完整对话内容

详见 [`10-宪章/开发流程规范.md`](10-宪章/开发流程规范.md) 与 [`10-宪章/安全红线.md`](10-宪章/安全红线.md)。

## 许可证

MIT License
