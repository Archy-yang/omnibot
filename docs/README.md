# OmniBot — 全平台智能助手

OmniBot 是一个个人 AI 助手项目。它把同一个助手接入到 Web、微信公众号、飞书等入口,
并围绕"长期对话"和"记忆管理"来探索一个持续在线、逐渐了解用户的 AI 助手应该怎么工作。

它不是一个多会话聊天工具。用户不需要新建对话、切换会话或管理会话列表。打开任意入口,
面对的都是同一个 OmniBot。

---

## 现在能做什么

### Web 聊天

- 单一长期对话界面
- Markdown 渲染
- Agent 工具调用过程展示
- 顶部导航 + 右侧抽屉式设置/记忆管理

### 长期记忆

- 添加、编辑、删除长期记忆
- 记忆与普通聊天历史分开管理
- 对话时自动把相关记忆注入上下文

### 自定义模型配置

- 在 Web 端配置自己的 LLM provider、模型、API Key、Base URL
- 支持 OpenAI Compatible 服务商
- API Key 加密存储

### Agent 工具调用

当前内置工具包括:

- 查询当前时间
- 数学计算
- 搜索长期记忆
- 搜索对话历史
- 读取 RSS

### 多入口接入

- Web 网页
- 微信公众号
- 飞书机器人(长连接)

不同入口最终会落到同一个用户身份上,共享同一份记忆和对话上下文。

---

## 技术栈

- 后端:Go + Gin + GORM
- 数据库:SQLite / PostgreSQL
- 前端:Vue 3 + TypeScript + Vite + Pinia
- LLM 协议:OpenAI Compatible
- 架构风格:DDD 分层(API / Service / Repository / Domain)

---

## 快速启动

复制配置模板:

```bash
cp configs/config.example.yaml configs/config.yaml
```

启动开发环境:

```bash
./start.sh dev
```

默认地址:

- Web 前端:http://localhost:5173/
- 后端 API:http://localhost:8080/

运行测试:

```bash
go test ./...
```

构建:

```bash
./start.sh build
```

---

## 文档导航

```text
docs/
├── 10-宪章/       项目愿景、开发约定、核心原则
├── 20-产品PRD/    产品需求文档
├── 30-服务架构/   架构设计与演进路线
├── 40-踩坑记录/   问题复盘与经验记录
├── 50-测试/       测试计划与测试报告
├── 60-设计/       页面设计原型与交互逻辑
└── 90-迭代记录/   CHANGELOG 与迭代记录
```

几个重要入口:

- [产品愿景](10-宪章/product-vision-v2.0.md)
- [单一长期对话模型](10-宪章/单一长期对话模型.md)
- [架构演进路线图](30-服务架构/01-高层设计/02-演进路线图-v3.0.md)
- [V2.0 目标交互逻辑](60-设计/02-V2.0目标交互逻辑.md)

---

## 项目状态

OmniBot 目前是一个持续迭代中的个人项目/技术实践项目。当前重点是:

- 长对话体验
- 记忆管理
- 多入口接入
- Agent 工具调用
- Web 端交互体验

---

## License

MIT License
