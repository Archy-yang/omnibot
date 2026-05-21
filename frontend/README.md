# OmniBot 前端

基于 Vue 3 + TypeScript + Vite 构建的智能助手 Web 界面。

## 技术栈

- **框架**: Vue 3 (Composition API) + TypeScript
- **构建工具**: Vite
- **状态管理**: Pinia (带持久化插件)
- **路由**: Vue Router
- **UI 组件库**: Naive UI + Tailwind CSS
- **HTTP 客户端**: Axios
- **代码规范**: ESLint + Prettier

## 项目结构

```
frontend/
├── src/
│   ├── components/          # 组件目录
│   │   ├── common/         # 基础组件
│   │   ├── chat/           # 聊天相关组件
│   │   └── layout/         # 布局组件
│   ├── composables/        # Vue Composables
│   ├── stores/             # Pinia 状态管理
│   ├── services/           # API 服务层
│   ├── types/              # TypeScript 类型定义
│   ├── views/              # 页面组件
│   ├── utils/              # 工具函数
│   ├── assets/             # 静态资源
│   ├── App.vue             # 根组件
│   ├── main.ts             # 入口文件
│   └── style.css           # 全局样式
├── embed.go               # Go Embed 配置
├── vite.config.ts         # Vite 配置
└── package.json
```

## 功能特性

### 1. 聊天功能
- 实时消息发送与显示
- 支持 Markdown 渲染
- 消息历史记录加载
- 发送中状态显示
- 错误处理与提示

### 2. 会话管理
- 自动生成会话 ID
- 会话持久化 (localStorage)
- 刷新页面后状态保持
- 支持新建会话

### 3. 设置功能
- 明暗主题切换
- 主题状态持久化
- LLM 配置面板
- 响应式布局适配

### 4. 架构设计
- 分层架构：Store → Composable → Component
- 类型安全：完整的 TypeScript 类型定义
- 统一错误处理：Axios 拦截器统一处理 API 错误
- 状态持久化：Pinia 插件自动持久化关键状态

## 开发指南

### 安装依赖

```bash
npm install
```

### 开发模式

```bash
npm run dev
```

开发服务器默认运行在 `http://localhost:5173`

**注意**: 开发模式下 API 请求会通过 Vite 代理转发到 `http://localhost:8080/api/v1`，需要确保后端服务正常运行。

### 代码检查

```bash
npm run lint
```

### 代码格式化

```bash
npm run format
```

### 生产构建

```bash
npm run build
```

构建产物输出到 `dist/` 目录。

## API 配置

API 请求基础路径: `/api/v1`

主要 API 端点：

- `GET /chat/messages` - 获取聊天历史
- `POST /chat/messages` - 发送消息
- `GET /config` - 获取配置
- `PUT /config` - 更新配置

## 状态管理

### Chat Store
- 消息列表管理
- 发送状态管理
- 会话 ID 同步

### User Store
- 会话管理
- 认证状态
- 持久化存储

### Settings Store
- 主题配置 (light/dark)
- LLM 配置
- 设置面板显示控制

## Go Embed 集成

前端构建产物会通过 Go Embed 嵌入到后端二进制文件中：

1. 执行 `npm run build` 构建前端
2. 构建 Go 后端时自动嵌入 `dist/` 目录
3. 访问 `/chat/` 路径提供前端页面

## 部署

### 单体部署（推荐）

前后端编译为单个二进制文件：

```bash
# 1. 构建前端
cd frontend
npm run build
cd ..

# 2. 构建 Go 后端（自动嵌入前端）
go build -o omnibot cmd/server/main.go

# 3. 运行
./omnibot -config configs/config.yaml
```

### 独立部署

前端作为静态资源部署：

```bash
npm run build
# 将 dist/ 目录部署到 Nginx 等 Web 服务器
```

## 浏览器兼容性

- Chrome/Edge (最新版)
- Firefox (最新版)
- Safari (最新版)
- 移动端浏览器适配

## 开发规范

1. 使用 `<script setup>` 语法
2. 遵循 Composition API 最佳实践
3. 所有 API 响应和状态数据定义 TypeScript 类型
4. Composable 负责逻辑封装，Component 专注视图渲染
5. Store 负责跨组件状态共享

## License

MIT
