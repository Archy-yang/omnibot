# PRD：OpenAI API 兼容客户端

## 需求背景

当前系统已支持通义千问和字节豆包两个大模型厂商，但缺少对 OpenAI API 协议的原生支持。OpenAI API 已成为行业事实标准，支持 ChatGPT、GPT-4、Azure OpenAI、Claude（通过兼容层）、以及大量开源模型（如 Llama、Qwen、ChatGLM 等）。

添加 OpenAI API 兼容客户端将大幅扩展系统的模型支持范围，使用户可以灵活选择最适合业务场景的大模型服务，同时简化新增兼容厂商的集成成本。

## 业务目标

配置 OpenAI/Azure/任意兼容服务 → 系统正确调用 API → 获取生成回复 → 支持自动降级

## 用户故事

| 角色 | 动作 | 期望 |
|------|------|------|
| 系统管理员 | 配置 OpenAI API Key | 系统能够正常调用 ChatGPT 生成回复 |
| 系统管理员 | 配置 Azure OpenAI 服务 | 系统能够正常调用 Azure OpenAI 生成回复 |
| 系统管理员 | 配置开源模型服务（兼容 OpenAI API） | 系统能够正常调用开源模型生成回复 |
| 系统管理员 | 将 OpenAI 设为默认 Provider | 系统优先使用 OpenAI 处理请求 |
| 开发者 | 新增其他 OpenAI 兼容厂商 | 只需配置 base_url，无需修改代码 |

## 功能需求

### 1. OpenAI Provider 实现

- 新增 `OpenAIProvider` 结构体，实现 `LLMProvider` 接口
- 构造函数签名：`NewOpenAIProvider(apiKey, baseURL, model string, timeout time.Duration) *OpenAIProvider`
- 实现 `ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)` 方法

### 2. 请求格式规范

- 遵循 OpenAI API v1 标准：`POST /chat/completions`
- 请求体包含：`model`、`messages` 字段
- messages 格式：`{"role": "system/user/assistant", "content": "文本"}`
- 支持 `stream: false`（与现有接口保持一致，非流式）

### 3. 认证方式

- 使用 Bearer Token 认证：`Authorization: Bearer {apiKey}`
- Content-Type：`application/json`

### 4. 响应解析

- 成功响应：从 `choices[0].message.content` 提取回复文本
- 错误响应：从 `error.message` 提取错误信息，返回 Go error

### 5. 工厂集成

- 修改 `llm.NewClient` factory，支持 provider name:
  - `openai`: OpenAI 官方 API
  - `azure`: Azure OpenAI Service（复用同一实现，通过 base_url 区分）
- 与现有 qwen/doubao provider 完全兼容，不影响现有逻辑

### 6. 配置支持

- `api_key`: API 密钥
- `base_url`: API 端点（可选，默认为 `https://api.openai.com/v1`）
- `model`: 模型名称（如 `gpt-3.5-turbo`）
- `timeout`: 请求超时时间

## 非功能需求

- 零第三方依赖：仅使用标准库 `net/http`（与 qwen/doubao 保持一致）
- 超时控制：支持独立配置超时时间
- 错误处理：统一返回 Go error，与现有 provider 行为一致
- 可测试性：代码结构支持使用 httptest 进行单元测试

## 验收标准

- [x] OpenAIProvider 正确实现 LLMProvider 接口
- [x] 支持标准 OpenAI API 调用 ChatGPT 成功
- [x] 支持 Azure OpenAI Service 成功
- [x] 支持配置自定义 base_url（兼容开源模型服务）
- [x] API 调用失败时正确返回错误信息
- [x] 工厂正确识别 "openai" 和 "azure" provider 类型
- [x] 与现有 qwen/doubao provider 完全兼容，不影响现有逻辑
- [x] 单元测试全部通过（5个用例）
- [x] 架构文档更新完成
- [x] 测试计划文档更新完成

## 依赖关系

- 依赖 `internal/client/llm/types.go` 已定义的 `LLMProvider` 接口和 `ChatMessage` 结构体
- 需要修改 `internal/client/llm/factory.go` 添加新 provider 注册
- 新增文件 `internal/client/llm/openai.go`
- 新增测试文件 `internal/client/llm/openai_test.go`

## 上线标准

- 所有验收标准通过
- 代码编译通过
- 单元测试全部通过
- 文档同步更新完成
