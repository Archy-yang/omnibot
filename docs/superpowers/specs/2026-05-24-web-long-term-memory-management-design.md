# Web 端长期记忆管理技术设计

## 背景

Web 端长期记忆管理基于 `docs/20-产品PRD/in_progress/Web端长期记忆管理PRD-v1.0.md`，目标是在现有 Web 聊天页面中提供长期记忆查看、新增、清空能力，并保持普通 Web 聊天继续自动使用最近 10 条长期记忆。

长期记忆领域能力已经在后端完成：`internal/domain/memory`、`internal/repository/memory`、`internal/service/memory` 和 `internal/service/chat.MessageService`。本设计不新增记忆模型，不新增独立 Web-only 记忆，只为 Web 入口补齐管理 API 和前端 UI。

## 目标

1. Web 后端提供长期记忆管理 API：查看、新增、清空。
2. Web 前端在现有设置弹窗中新增 `长期记忆` 分区。
3. 用户可以在 Web 端查看全部长期记忆。
4. 用户可以新增一条 200 字以内的长期记忆。
5. 用户可以二次确认后清空全部长期记忆。
6. 普通 Web 聊天继续通过 `MessageService.BuildContextMessages` 注入最近 10 条长期记忆。
7. Web 端复用已有 `memory.MemoryService`，不绕过 Service 访问 Repository 或 DB。
8. 日志不输出完整记忆内容或完整用户对话内容。

## 非目标

1. 不做单条删除。
2. 不做单条编辑。
3. 不做分页、搜索、分类、置顶。
4. 不做自动记忆提取。
5. 不做向量召回。
6. 不新增独立记忆中心页面或新路由。
7. 不引入新的前端依赖。
8. 不改变现有微信记忆命令。
9. 不改变长期记忆数据表结构。

## 架构

采用最小增量方案：后端在现有 `internal/api/web.Handler` 中注入 `memory.MemoryService`，新增 Web memory API；前端新增 memory service/store/composable/component，并挂载到现有 `SettingsPanel.vue`。

```text
frontend SettingsPanel
  ↓
frontend MemorySection component
  ↓
frontend useMemory / memoryStore
  ↓
frontend memoryService
  ↓
GET/POST/DELETE /api/v1/memories
  ↓
internal/api/web.Handler
  ↓
internal/service/memory.MemoryService
  ↓
internal/repository/memory.MemoryRepository
  ↓
memories table
```

普通聊天链路保持现状：

```text
frontend chatService.sendMessage
  ↓
POST /api/v1/chat/messages
  ↓
internal/api/web.Handler.HandleSendMessage
  ↓
MessageService.BuildContextMessages
  ↓
MemoryService.GetRecentForContext(userID, 10)
```

## 后端设计

### Web Handler 依赖

扩展 `internal/api/web.Handler`，新增长期记忆服务依赖。

```go
type MemoryService interface {
    Remember(ctx context.Context, userID int64, content string) (*memory.Memory, error)
    List(ctx context.Context, userID int64) ([]*memory.Memory, error)
    Clear(ctx context.Context, userID int64) error
}

type Handler struct {
    userService      UserService
    messageService   MessageService
    llmClient        LLMClient
    llmConfigService LLMConfigService
    memoryService    MemoryService
}
```

`NewHandler` 增加 `memoryService MemoryService` 参数。`routes.go` 已经创建 `memorySvc`，因此直接传入 Web handler。

### API 路由

在 `internal/api/routes.go` 的 `/api/v1` 下新增 memories 路由：

```text
GET    /api/v1/memories?session_id=xxx
POST   /api/v1/memories
DELETE /api/v1/memories?session_id=xxx
```

不放在 `/api/v1/chat` 下，因为长期记忆是用户能力，不是聊天消息能力。

### 请求与响应

#### 获取记忆列表

请求：

```http
GET /api/v1/memories?session_id=xxx
```

响应：

```json
{
  "success": true,
  "data": {
    "memories": [
      {
        "id": 1,
        "content": "我偏好简洁直接的回答",
        "created_at": "2026-05-24T10:00:00Z"
      }
    ]
  }
}
```

空状态返回：

```json
{
  "success": true,
  "data": {
    "memories": []
  }
}
```

#### 新增记忆

请求：

```http
POST /api/v1/memories
Content-Type: application/json
```

```json
{
  "session_id": "web-session-id",
  "content": "我偏好简洁直接的回答"
}
```

响应：

```json
{
  "success": true,
  "data": {
    "message": "已记住。",
    "memory": {
      "id": 1,
      "content": "我偏好简洁直接的回答",
      "created_at": "2026-05-24T10:00:00Z"
    }
  }
}
```

#### 清空记忆

请求：

```http
DELETE /api/v1/memories?session_id=xxx
```

响应：

```json
{
  "success": true,
  "data": {
    "message": "已清空你的全部长期记忆。"
  }
}
```

### DTO

在 `internal/api/web/handler.go` 中新增轻量 DTO。由于当前 Web handler 仍集中承载 Web API，MVP 不拆新 handler 文件；若文件超过代码风格限制，实施时可拆成 `memory_handler.go`，包名仍为 `web`。

```go
type MemoryDTO struct {
    ID        int64  `json:"id"`
    Content   string `json:"content"`
    CreatedAt string `json:"created_at"`
}

type GetMemoriesRequest struct {
    SessionID string `form:"session_id" binding:"required"`
}

type GetMemoriesResponse struct {
    Memories []MemoryDTO `json:"memories"`
}

type CreateMemoryRequest struct {
    SessionID string `json:"session_id" binding:"required"`
    Content   string `json:"content" binding:"required"`
}

type CreateMemoryResponse struct {
    Message string    `json:"message"`
    Memory  MemoryDTO `json:"memory"`
}

type ClearMemoriesRequest struct {
    SessionID string `form:"session_id" binding:"required"`
}

type ClearMemoriesResponse struct {
    Message string `json:"message"`
}
```

### Handler 行为

新增方法：

```go
func (h *Handler) HandleGetMemories(c *gin.Context)
func (h *Handler) HandleCreateMemory(c *gin.Context)
func (h *Handler) HandleClearMemories(c *gin.Context)
```

公共流程：

1. 绑定请求参数。
2. 通过 `userService.GetOrCreateByChannel("web", sessionID)` 获取用户。
3. 调用 `memoryService`。
4. 返回统一 Web API 响应。

用户获取失败返回：

```json
{
  "success": false,
  "error": "服务暂时不可用，请稍后再试。"
}
```

### 错误映射

`HandleCreateMemory` 需要识别 service sentinel error：

| Service 错误 | HTTP 状态 | Web 文案 |
|--------------|-----------|----------|
| `memory.ErrEmptyContent` | 400 | `请输入要长期记住的内容。` |
| `memory.ErrContentTooLong` | 400 | `这条记忆太长了，请控制在 200 字以内。` |
| 其他错误 | 500 | `服务暂时不可用，请稍后再试。` |

`HandleGetMemories` 和 `HandleClearMemories` 的 service 错误统一返回 500 和 `服务暂时不可用，请稍后再试。`。

缺少 `session_id` 或 JSON 格式错误返回 400。对外不返回 binding 细节，避免泄露内部字段校验实现。

### 日志

Web memory handler 不记录完整记忆内容。

允许记录：

- `user_id`
- `memory_id`
- `content_length`
- `operation`
- `error`

新增失败日志只记录操作和错误，不记录 `content`。

### 依赖装配

`internal/api/routes.go` 修改：

```go
webHandler := web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc)
```

新增路由：

```go
memoryAPIGroup := r.Group("/api/v1/memories")
{
    memoryAPIGroup.GET("", webHandler.HandleGetMemories)
    memoryAPIGroup.POST("", webHandler.HandleCreateMemory)
    memoryAPIGroup.DELETE("", webHandler.HandleClearMemories)
}
```

## 前端设计

### 文件结构

新增和修改文件：

```text
frontend/src/types/api.ts
frontend/src/services/memory.ts
frontend/src/stores/memory.ts
frontend/src/composables/useMemory.ts
frontend/src/components/functional/MemorySection.vue
frontend/src/components/functional/SettingsPanel.vue
```

职责：

- `types/api.ts`：定义 Web memory API 类型。
- `services/memory.ts`：封装 `/memories` API 调用。
- `stores/memory.ts`：管理长期记忆列表、加载状态、保存状态、清空状态。
- `composables/useMemory.ts`：给组件提供稳定 API，隐藏 store 细节。
- `MemorySection.vue`：渲染长期记忆 UI。
- `SettingsPanel.vue`：在设置弹窗中挂载长期记忆分区。

### TypeScript 类型

在 `frontend/src/types/api.ts` 新增：

```ts
export interface MemoryItem {
  id: number;
  content: string;
  created_at: string;
}

export interface GetMemoriesResponse {
  memories: MemoryItem[];
}

export interface CreateMemoryRequest {
  session_id: string;
  content: string;
}

export interface CreateMemoryResponse {
  message: string;
  memory: MemoryItem;
}

export interface ClearMemoriesResponse {
  message: string;
}
```

不使用 `any`。

### Service

新增 `frontend/src/services/memory.ts`。

```ts
import { request } from '../utils/request';
import type {
  ApiResponse,
  ClearMemoriesResponse,
  CreateMemoryRequest,
  CreateMemoryResponse,
  GetMemoriesResponse,
} from '../types/api';

export const memoryService = {
  async getMemories(sessionId: string): Promise<GetMemoriesResponse> {
    const response = await request.get<ApiResponse<GetMemoriesResponse>>('/memories', {
      params: { session_id: sessionId },
    });
    return response.data.data;
  },

  async createMemory(requestBody: CreateMemoryRequest): Promise<CreateMemoryResponse> {
    const response = await request.post<ApiResponse<CreateMemoryResponse>>('/memories', requestBody);
    return response.data.data;
  },

  async clearMemories(sessionId: string): Promise<ClearMemoriesResponse> {
    const response = await request.delete<ApiResponse<ClearMemoriesResponse>>('/memories', {
      params: { session_id: sessionId },
    });
    return response.data.data;
  },
};

export default memoryService;
```

Service 不直接操作 toast、不读写 localStorage，只封装 API。

### Store

新增 `frontend/src/stores/memory.ts`。

状态：

```ts
const memories = ref<MemoryItem[]>([]);
const isLoading = ref(false);
const isCreating = ref(false);
const isClearing = ref(false);
```

Actions：

```ts
loadMemories(): Promise<void>
createMemory(content: string): Promise<CreateMemoryResponse>
clearMemories(): Promise<ClearMemoriesResponse>
```

Store 通过 `useSession()` 获取 `sessionId`，与现有 settings store 的会话模式保持一致。

行为规则：

- `loadMemories` 成功后覆盖 `memories`。
- `createMemory` 成功后把返回的 `memory` 追加到 `memories`。
- `clearMemories` 成功后把 `memories` 置空。
- Store 不做复杂前端校验，空内容 trim 可在组件提交前处理；后端仍是最终边界。
- 不持久化 memories 到 localStorage，避免长期记忆在浏览器残留。

### Composable

新增 `frontend/src/composables/useMemory.ts`。

职责：

- 暴露 `memories`、loading 状态。
- 暴露 `loadMemories`、`createMemory`、`clearMemories`。
- 通过 computed 保持只读状态接口。

`frontend/src/composables/index.ts` 如已有集中导出，需要增加 `useMemory` 导出。

### UI 组件

新增 `frontend/src/components/functional/MemorySection.vue`。

使用 `<script setup lang="ts">`。

UI 结构：

```text
长期记忆
  NAlert: 请不要保存密码、API Key、身份证号等敏感信息。
  NInput type=textarea: 输入希望助手长期记住的内容
  NButton: 保存
  NSpin / loading state
  Empty state: 我还没有长期记住任何信息。
  NList: 记忆列表
  NPopconfirm + NButton type=error: 清空全部长期记忆
```

组件行为：

- 组件 mounted 时调用 `loadMemories`。
- 保存按钮点击时 trim 输入。
- 空内容直接 toast：`请输入要长期记住的内容。`
- 超过 200 个 Unicode 字符直接 toast：`这条记忆太长了，请控制在 200 字以内。`
- 保存成功后清空输入框，toast `已记住。` 和敏感信息提醒。
- 后端错误直接展示错误消息；无法识别时展示 `服务暂时不可用，请稍后再试。`。
- 清空按钮使用 `NPopconfirm` 二次确认。
- 清空成功后 toast `已清空你的全部长期记忆。`。

Unicode 长度用展开字符串计算：

```ts
const memoryLength = computed(() => [...memoryInput.value.trim()].length);
```

不使用 `v-html` 渲染记忆内容，直接插值展示，避免 XSS。

### SettingsPanel 集成

修改 `frontend/src/components/functional/SettingsPanel.vue`：

1. import `MemorySection`。
2. 在 LLM 配置分区之后增加：

```vue
<NDivider title-placement="left">长期记忆</NDivider>
<MemorySection />
```

不改变现有 LLM 配置保存、清除逻辑。

## 前端错误处理

后端 Web API 当前错误响应是：

```json
{
  "success": false,
  "error": "错误文案"
}
```

`utils/request.ts` 响应拦截器会把 `error` 转成 `Error.message`。Memory UI 捕获错误后使用 `err instanceof Error ? err.message : "服务暂时不可用，请稍后再试。"`。

为了避免前端控制台泄露记忆内容，新增 memory service/store/component 不打印包含 content 的对象。

## 测试策略

所有实现按 TDD 执行。

### 后端测试

新增或扩展 `internal/api/web/handler_test.go`。

覆盖：

1. `GET /api/v1/memories` 有记忆时返回列表。
2. `GET /api/v1/memories` 无记忆时返回空数组。
3. `GET /api/v1/memories` 缺少 `session_id` 返回 400。
4. `POST /api/v1/memories` 成功创建记忆。
5. `POST /api/v1/memories` 空内容返回 `请输入要长期记住的内容。`。
6. `POST /api/v1/memories` 超长内容返回 `这条记忆太长了，请控制在 200 字以内。`。
7. `POST /api/v1/memories` service 失败返回 `服务暂时不可用，请稍后再试。`。
8. `DELETE /api/v1/memories` 成功清空记忆。
9. `DELETE /api/v1/memories` 缺少 `session_id` 返回 400。
10. 不同 session/user 的记忆隔离。
11. Web 普通聊天仍通过 `BuildContextMessages` 调用，不因新增 memory API 破坏现有测试。

如 `handler_test.go` 过大，实施时新增 `internal/api/web/handler_memory_test.go`，包名仍为 `web`。

Mock 扩展：

- `mockMemoryService` 实现 `Remember`、`List`、`Clear`。
- 记录调用的 `userID` 和 `content`，用于验证用户隔离和 trim 后保存。

### 前端测试

如果当前前端测试框架已可用，新增组件和 store 测试：

1. `memoryService` 使用 request mock 验证路径和参数。
2. `memoryStore.loadMemories` 成功后更新列表。
3. `memoryStore.createMemory` 成功后追加记忆。
4. `memoryStore.clearMemories` 成功后清空列表。
5. `MemorySection` 空状态展示。
6. `MemorySection` 空内容不调用 store。
7. `MemorySection` 超过 200 Unicode 字符不调用 store。
8. `MemorySection` 保存成功清空输入。
9. `MemorySection` 清空前需要确认。

如果当前前端没有测试框架或测试基础不足，实施计划中先用 `npm run type-check`、`npm run lint`、`npm run build` 和浏览器手动验证覆盖 Web UI 验收路径，不在本功能中引入新测试依赖。

### 手动验收

必须启动服务并在浏览器验证：

1. 打开 Web 聊天页。
2. 打开设置。
3. 查看长期记忆空状态。
4. 新增一条记忆。
5. 列表显示新增记忆。
6. 输入空内容，确认提示且不保存。
7. 输入超过 200 字，确认提示且不保存。
8. 清空记忆，确认需要二次确认。
9. 清空后回到空状态。
10. 发送普通聊天，确认聊天仍可正常回复。

## 验收映射

| PRD 验收项 | 技术设计覆盖 |
|------------|--------------|
| Web 用户可以打开长期记忆管理入口 | `SettingsPanel.vue` 挂载 `MemorySection.vue` |
| 无记忆时展示空状态 | `MemorySection.vue` 空列表状态 |
| 有记忆时展示完整记忆列表 | `GET /api/v1/memories` + `memoryStore.memories` |
| 用户可以新增记忆 | `POST /api/v1/memories` + `createMemory` |
| 新增成功后列表刷新 | store 追加返回 memory 或重新加载列表 |
| 空内容不能保存 | 前端 trim 校验 + 后端 `ErrEmptyContent` 映射 |
| 超过 200 字不能保存 | 前端 Unicode 长度校验 + 后端 `ErrContentTooLong` 映射 |
| 新增成功后展示敏感信息提醒 | `MemorySection.vue` 成功 toast/alert |
| 清空前二次确认 | `NPopconfirm` |
| 清空成功后列表为空 | `DELETE /api/v1/memories` + store 清空 |
| 不影响短期聊天历史 | 不修改消息存储和 chat store |
| 不影响用户自定义 LLM 配置 | `SettingsPanel.vue` 只新增分区，不改现有配置 API |
| Web 普通聊天继续注入最近 10 条记忆 | 继续使用 `MessageService.BuildContextMessages` |
| 记忆查询失败普通聊天仍继续 | 既有 message service 降级策略 |
| 用户隔离 | 后端通过 `session_id` 获取 `UserID`，service 按 userID 查询 |
| 日志不输出完整记忆内容 | handler/service 日志字段约束 |
| 微信公众号记忆命令不受影响 | 不修改 wechat handler |

## 实施顺序

1. 后端 Web memory API 测试。
2. 后端 Web handler memory API 实现。
3. routes 依赖注入和路由注册。
4. 前端 memory API 类型和 service 测试/实现。
5. 前端 memory store/composable 测试/实现。
6. 前端 `MemorySection.vue` 测试/实现。
7. `SettingsPanel.vue` 集成长期记忆分区。
8. 后端和前端完整验证。
9. 浏览器手动验收 Web UI。

