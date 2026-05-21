## 1. 核心实现

- [x] 1.1 创建 `internal/client/llm/openai.go`，定义 OpenAIProvider 结构体
- [x] 1.2 实现 NewOpenAIProvider 构造函数（apiKey, baseURL, model, timeout）
- [x] 1.3 实现 ChatCompletion 方法（构造请求、发送 HTTP、解析响应）
- [x] 1.4 实现成功响应解析（从 choices[0].message.content 提取）
- [x] 1.5 实现错误响应解析（从 error.message 提取错误信息）

## 2. 工厂集成

- [x] 2.1 修改 `internal/client/llm/factory.go`，添加 "openai" 类型识别
- [x] 2.2 添加 "azure" 类型识别（复用 OpenAIProvider）
- [x] 2.3 确保与现有 qwen/doubao 工厂逻辑兼容

## 3. 单元测试

- [x] 3.1 创建 `internal/client/llm/openai_test.go`
- [x] 3.2 实现 TestOpenAI_NewProvider：验证构造函数初始化
- [x] 3.3 实现 TestOpenAI_ParseResponse：验证成功响应解析
- [x] 3.4 实现 TestOpenAI_ParseError：验证错误响应解析
- [x] 3.5 实现 TestOpenAI_HTTPRequest：使用 httptest 模拟 API 服务器
- [x] 3.6 运行所有测试，确保通过

## 4. 文档更新

- [x] 4.1 创建 `docs/architecture/internal/client/llm/openai.md` 架构文档
- [x] 4.2 创建 `docs/testing/test-plans/internal/client/llm/openai.md` 测试计划文档
- [x] 4.3 配置示例说明（OpenAI、Azure 配置格式）
