# CLAUDE.md

微信公众号智能对话机器人 - 项目开发规范文档

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

---

## 目录

1. [核心开发流程](#核心开发流程)
2. [文档规范与目录结构](#文档规范与目录结构)
3. [TDD 测试驱动开发](#tdd-测试驱动开发)
4. [架构分层规范](#架构分层规范)
5. [数据库设计规范](#数据库设计规范)
6. [安全编码规范](#安全编码规范)
7. [常用构建命令](#常用构建命令)
8. [项目特定信息](#项目特定信息)

---

## 核心开发流程

### Development Constraints [CRITICAL WORKFLOW]

#### 1. 修改前必须先出方案
任何代码改动之前，先输出修改方案说明：
- 改动背景和原因
- 涉及的文件列表
- 具体的修改思路
- 可能的影响范围
等待用户确认后再实施

#### 2. 复杂任务拆解
对于复杂功能，必须拆解为多个子任务：
- 使用 TaskCreate / TaskUpdate 进行任务追踪
- 每个子任务独立实现、测试、提交
- 完成一个子任务后再进行下一个

#### 3. 最小改动原则
以最小改动完成当前目标：
- 不私自进行额外的重构或优化
- 不在功能开发中混入无关的代码清理
- 重构需求单独提出来，单独作为任务处理

#### 4. 文档同步更新
**严格按顺序执行文档更新：**
- **测试计划文档**：在写测试代码之前更新（TDD 流程要求）
- **PRD 文档**：开发前确认需求是否已在 PRD 中定义
- **架构文档**：代码修改完成后更新，保证文档描述与代码实现一致
- **CHANGELOG**：版本发布前更新版本变更记录

#### 5. Subagent-Driven Development
对于迭代级别的开发任务：
- 使用 superpowers:subagent-driven-development 技能
- 每个子任务指派独立的 subagent 实现
- 实现后进行 spec 合规审查 + 代码质量审查
- 双审查通过后才合并到主线

---

## 文档规范与目录结构

### Docs 目录结构（按 10-90 分类）

```
docs/
├── 10-宪章/              # 项目宪章、愿景、核心原则
│   └── README.md
│
├── 20-产品PRD/           # 产品需求文档
│   ├── backlog/          # 待排期需求池
│   ├── in_progress/      # 开发中的需求（当前迭代）
│   ├── completed/        # 已上线的需求归档
│   └── README.md         # PRD 列表索引
│
├── 30-服务架构/           # 系统架构设计文档
│   ├── 00-系统设计总览.md  # 整体架构、技术选型、路线图
│   ├── current-capabilities.md  # 当前已实现能力总览
│   ├── progress.md       # 实现进度追踪
│   ├── project-overview.md  # 项目概览
│   ├── overall.md        # 架构总览图
│   └── internal/         # 按代码目录结构的模块级架构文档
│       ├── api/          # API 层文档
│       ├── service/      # 服务层文档
│       ├── repository/   # 数据访问层文档
│       ├── domain/       # 领域实体文档
│       ├── client/       # 外部客户端文档
│       ├── db/           # 数据库文档
│       ├── middleware/   # 中间件文档
│       └── pkg/          # 公共包文档
│
├── 40-踩坑记录/           # 开发过程中遇到的问题和解决方案
│   └── README.md
│
├── 50-测试/               # 测试相关文档
│   ├── README.md
│   └── test-plans/       # 测试计划，目录结构与代码完全一致
│       ├── internal/
│       │   ├── api/
│       │   ├── service/
│       │   ├── repository/
│       │   └── domain/
│       └── vX.X-功能名_test.md  # 迭代级别的整体测试计划
│
├── 90-迭代记录/           # 版本迭代记录
│   ├── archived/         # 已完成的迭代归档
│   │   └── vX.X-功能名/
│   │       ├── README.md
│   │       ├── 技术方案.md
│   │       └── 迭代复盘.md
│   ├── CHANGELOG.md      # 版本变更日志（按版本号倒序排列）
│   ├── RELEASES.md       # 发布记录
│   └── README.md
│
└── superpowers/          # 超级能力实现计划
    └── plans/            # 按迭代拆分的详细实现计划
```

### 文档编写规范

#### 架构文档规范
`docs/30-服务架构/internal/<path>/<filename>.md` 每个代码文件对应一个架构文档，必须包含：
- **模块职责**：这个文件/模块负责什么功能
- **入口函数**：主要的公开函数/方法签名
- **处理流程**：核心业务流程的文字描述或流程图
- **已实现能力**：当前已完成的功能列表
- **依赖关系**：依赖的其他模块
- **数据结构**：核心结构体定义（如果有）

#### 测试计划文档规范
`docs/50-测试/test-plans/<path>/<filename>_test.md` 每个测试文件对应一个测试计划文档。

每个测试方法单独一个区块：
````markdown
---
## 方法：TestName

**测试目的**：一句话说明测试要验证的功能

**输入参数**：
- 参数名：参数说明

**期望断言**：
- 断言1的具体描述
- 断言2的具体描述
---
````

#### CHANGELOG 规范
每发布一个版本，在 `docs/90-迭代记录/CHANGELOG.md` 顶部新增记录：
- ✨ 新增功能
- 🔧 架构改进
- 🐛 Bug 修复
- 🔐 安全增强
- 📦 数据变更

---

## TDD 测试驱动开发

### TDD 标准流程

```
1. 编写/更新测试计划文档 → docs/50-测试/test-plans/...
       ↓
2. 编写测试代码 → 运行测试 → 确认测试失败（RED）
       ↓
3. 编写最小实现代码 → 运行测试 → 确认测试通过（GREEN）
       ↓
4. 必要的重构（不改变功能行为）→ 测试仍然通过（REFACTOR）
       ↓
5. 更新架构文档 → docs/30-服务架构/...
       ↓
6. 提交代码
```

### 测试分层规范

#### 1. 单元测试
- 测试单个函数/方法的行为
- 使用 mock 隔离外部依赖
- 文件名：`*_test.go`，与被测文件同目录

#### 2. 集成测试
- 测试多个组件配合
- 使用真实的数据库连接（SQLite in-memory）
- 测试完整的业务流程

#### 3. 端到端测试
- 启动完整的 HTTP 服务器
- 通过 `net/http/httptest` 发送真实请求
- 不使用 mock handler，走完整的路由链

### Go 测试特定约束
- **HTTP 接口测试**：启动测试 HTTP 服务器，通过 `net/http/httptest` 配合真实路由进行模拟访问，不使用 mock 处理器
- **数据库测试**：使用 SQLite in-memory 替代 MySQL/PostgreSQL，避免外部依赖
- **Redis 测试**：使用 `miniredis` 替代真实 Redis，内存式测试
- **测试命名**：`Test<Module>_<Function>_<Scenario>`，例如：`TestLLMConfigService_SetAPIKey_InvalidFormat`

---

## 架构分层规范

本项目采用 DDD（领域驱动设计）分层架构：

```
┌─────────────────────────────────────────┐
│         API 层 (internal/api/)          │
│  Handler / Controller / Router          │
│  负责：请求解析、参数校验、响应格式化      │
└────────────────────┬────────────────────┘
                     │
┌────────────────────▼────────────────────┐
│       Service 层 (internal/service/)    │
│  Business Logic / Use Case              │
│  负责：业务逻辑编排、事务边界、权限校验    │
└────────────────────┬────────────────────┘
                     │
┌────────────────────▼────────────────────┐
│    Repository 层 (internal/repository/) │
│  Data Access Object                      │
│  负责：数据读写、查询构建、数据库操作      │
└────────────────────┬────────────────────┘
                     │
┌────────────────────▼────────────────────┐
│      Domain 层 (internal/domain/)       │
│  Entity / Value Object / Domain Service │
│  负责：领域模型定义、核心业务规则          │
└────────────────────┬────────────────────┘
                     │
┌────────────────────▼────────────────────┐
│    Infrastructure 层 (internal/db/)     │
│  Database / External Client             │
│  负责：数据库连接、外部服务客户端          │
└─────────────────────────────────────────┘
```

### 分层依赖规则
- API 层只能依赖 Service 层，不能直接依赖 Repository 或 DB
- Service 层只能依赖 Repository 层和 Domain 层
- Repository 层只能依赖 Domain 层和 DB 层
- Domain 层不依赖任何其他层（纯 Go struct + 方法）
- **所有跨层调用必须通过接口，不能直接依赖具体实现**

### 新增模块标准流程
1. 在 `internal/domain/<module>/` 定义领域实体和接口
2. 在 `internal/repository/<module>/` 实现 Repository
3. 在 `internal/service/<module>/` 实现 Service 逻辑
4. 在 `internal/api/<module>/` 实现 Handler 和路由
5. 配套的测试和文档同步更新

---

## 数据库设计规范

### 表命名规范
- 全小写，下划线分隔
- 复数形式：`users`, `wechat_accounts`, `user_llm_configs`
- 关联表：`<table1>_<table2>_rels`

### 必备字段
所有表必须包含：
```go
ID        int64     `gorm:"primaryKey;autoIncrement"`
CreatedAt time.Time `gorm:"not null"`
UpdatedAt time.Time `gorm:"not null"`
```

### AutoMigration 规范
- 所有表结构变更必须通过 GORM AutoMigration 实现
- 在 `internal/db/database.go` 的 `autoMigrate()` 函数中注册
- 新增表必须同时更新 `docs/30-服务架构/00-系统设计总览.md` 的数据存储章节
- 破坏性变更（删表、删字段、改类型）必须单独提方案评审

### 索引规范
- 外键字段必须建索引：`user_id`
- 唯一业务字段必须建唯一索引：`open_id`, `union_id`
- 查询条件字段必须建普通索引
- 复合索引顺序按区分度从高到低排列

---

## 安全编码规范

### 敏感数据处理

#### 1. 加密存储规范
- 用户 API Key、密码等敏感信息必须加密存储
- 使用 AES-256-GCM 认证加密（参考 `internal/pkg/crypto/aes.go`）
- 加密密钥必须通过环境变量配置，不能硬编码
- 环境变量名：`LLM_CONFIG_ENCRYPT_KEY`（32字节）

#### 2. 脱敏展示规范
前端/接口展示敏感信息时：
- API Key：只显示前后 3-4 位，中间用 `...` 替代
- 手机号：`138****1234`
- 邮箱：`use*****@domain.com`
- 绝对不能在日志中输出完整的敏感信息

#### 3. 日志安全规范
- 日志中不能输出用户敏感信息（API Key、密码、Token）
- 不能输出完整的用户消息内容
- 只能输出用户 ID、消息类型等非敏感元数据
- 使用 Zap 的带字段日志时，注意字段值的安全性

### 输入校验规范
所有用户输入必须校验：
- API Key：必须以 `sk-` 开头，长度 30-200
- URL：必须以 `http://` 或 `https://` 开头
- 字符串长度：所有字符串字段必须有合理的长度限制
- 枚举值：状态字段必须在预定义的枚举范围内

### 错误处理规范
- 对外返回的错误信息不能泄露内部实现细节
- 数据库错误统一包装成 "服务暂时不可用"
- 参数校验错误返回友好的提示信息
- 所有内部错误必须打 Error 级别日志，带 stack trace

---

## 常用构建命令

### Go 开发命令
```bash
# 构建
go build -o bin/wechat-bot cmd/main/main.go

# 运行（指定配置文件）
go run cmd/main/main.go -config configs/config.yaml

# 代码格式化
go fmt ./...

# 静态检查
go vet ./...

# 运行所有测试
go test ./...

# 运行单个测试
go test -v ./internal/service/user/... -run TestLLMConfigService_SetAPIKey

# 带覆盖率
go test -cover ./...
go test -coverprofile=coverage.out ./... && go tool cover -html=coverage.out
```

### Git 提交规范
提交信息格式：
```
<type>(<scope>): <subject>

类型 type：
- feat: 新功能
- fix: 修复 bug
- docs: 文档变更
- style: 代码格式调整
- refactor: 重构（不新增功能，不修 bug）
- test: 测试相关
- chore: 构建/工具相关

示例：
feat(llm-config): add AES encryption for API key storage
fix(wechat): handle empty message content gracefully
docs(changelog): update v1.1.0 release notes
```

---

## 项目特定信息

### Project Overview
本项目是基于 Go + Gin 框架开发的微信公众号智能对话机器人，支持：
- 微信公众号消息接入和回复
- 用户体系（关注自动创建账号）
- OpenAI 兼容的大模型对话
- **用户自定义 LLM 配置**（v1.1 新功能）
  - 用户可独立设置自己的 API Key
  - 可自定义 API 服务地址（兼容所有 OpenAI 格式服务）
  - 配置 AES-256-GCM 加密存储
  - 微信命令式配置交互

### 文档索引速查
- **项目总览**: `docs/30-服务架构/project-overview.md`
- **当前能力**: `docs/30-服务架构/current-capabilities.md`
- **架构总览**: `docs/30-服务架构/00-系统设计总览.md`
- **实现进度**: `docs/30-服务架构/progress.md`
- **需求管理**: `docs/20-产品PRD/`
- **测试计划**: `docs/50-测试/test-plans/`
- **变更日志**: `docs/90-迭代记录/CHANGELOG.md`
- **迭代记录**: `docs/90-迭代记录/archived/`

### 当前版本信息
- **稳定版本**: v1.1.0
- **当前迭代**: v1.2.0（规划中）
- **核心特性**: 用户自定义 LLM 配置

### graphify integration
本项目启用了 graphify 知识图谱，位于 `graphify-out/`。

使用规则：
- 回答架构或代码库问题前，先读取 `graphify-out/GRAPH_REPORT.md` 获取核心节点和社区结构
- 如果 `graphify-out/wiki/index.md` 存在，优先浏览它而不是逐个读原始文件
- 本会话中修改代码文件后，运行 `graphify update .` 更新图谱（仅 AST 分析，无 API 开销）
