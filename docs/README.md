# 微信公众号智能对话系统

## 项目介绍
这是一个集成了微信公众号、大模型服务和Memory OS记忆系统的智能对话系统。系统能够提供高度个性化的智能对话服务，并能记住用户的偏好和历史对话，建立长期的用户关系。

## 核心特性

### 1. 智能化对话
- 基于GPT-4、Claude等大模型的自然语言理解
- 上下文感知的多轮对话
- 意图识别和对话路由

### 2. 个性化服务
- Memory OS记忆系统存储用户偏好
- 历史对话场景记忆
- 基于用户画像的个性化回复

### 3. 微信公众号集成
- 完整的微信公众号接口支持
- 消息加解密和安全验证
- 多媒体消息处理（图片、语音、视频）

### 4. 系统可扩展性
- 模块化设计，易于扩展
- 支持多LLM厂商切换
- 分布式架构支持水平扩展

## 系统架构

详见[30-服务架构/00-系统设计总览.md](30-服务架构/00-系统设计总览.md)

## 文档目录结构

```
docs/
├── 10-宪章/             # 开发规范、团队约定、基本原则
├── 20-产品PRD/          # 产品需求文档，按状态分类
│   ├── backlog/         # 待排期需求池
│   ├── in_progress/     # 开发中需求
│   └── completed/       # 已上线需求归档
├── 30-服务架构/         # 技术架构、系统设计、模块文档
│   ├── internal/        # 与代码目录结构镜像
│   ├── pkg/             # 公共包文档
│   └── superpowers/     # 实现计划
├── 40-踩坑记录/         # 技术问题复盘与解决方案
├── 50-测试/             # 测试计划、测试用例、测试报告
│   ├── test-plans/      # 测试计划（与代码目录结构镜像）
│   └── test-reports/    # 测试覆盖率报告
└── 90-迭代记录/         # 版本迭代、变更日志、发布记录
```

## 快速开始

### 环境要求
- Go 1.21+
- Redis 6.0+
- PostgreSQL 14+
- Docker（可选）

### 配置说明
1. 复制配置文件模板
```bash
cp configs/config.example.yaml configs/config.yaml
```

2. 配置环境变量
```bash
export WECHAT_APP_ID=your_app_id
export WECHAT_APP_SECRET=your_app_secret
export OPENAI_API_KEY=your_openai_key
```

### 启动服务
```bash
# 开发模式
go run cmd/server/main.go

# 生产模式
docker-compose up -d
```

## 项目结构

```
omnibot/
├── cmd/                    # 可执行程序入口
├── internal/              # 私有应用代码
├── pkg/                   # 公共库代码
├── configs/               # 配置文件
├── deployments/           # 部署配置
├── scripts/              # 脚本文件
└── docs/                 # 文档
```

## 部署方式

### Docker部署
```bash
docker build -t wechat-bot .
docker run -p 8080:8080 wechat-bot
```

### Kubernetes部署
```bash
kubectl apply -f deployments/kubernetes/
```

### 传统部署
```bash
# 构建
make build

# 启动
./bin/omnibot
```

## API文档

### 微信回调接口
- `GET/POST /wechat/callback` - 微信服务器回调入口

### 管理接口
- `GET /api/v1/health` - 健康检查
- `GET /api/v1/users` - 用户列表
- `GET /api/v1/conversations` - 对话历史

## 监控和运维

系统提供以下监控指标：
- 请求量统计
- LLM API调用延迟
- 内存使用情况
- 数据库连接状态

## 开发指南

### 添加新的LLM厂商
1. 在 `internal/client/llm/` 目录下创建新的客户端
2. 实现 `LLMProvider` 接口
3. 更新配置文件支持新的厂商

### 添加新的消息处理器
1. 在 `internal/service/chat/` 目录下创建新的处理器
2. 在消息路由中注册处理器
3. 实现消息处理逻辑

## 贡献指南

1. Fork 项目
2. 创建功能分支
3. 提交代码变更
4. 创建 Pull Request

## 许可证

MIT License
