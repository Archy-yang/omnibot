## Why

当前系统已支持通义千问和字节豆包两个大模型厂商，但缺少对 OpenAI API 协议的原生支持。OpenAI API 已成为行业事实标准，支持 ChatGPT、GPT-4、Azure OpenAI、Claude（通过兼容层）、以及大量开源模型（如 Llama、Qwen、ChatGLM 等）。

添加 OpenAI API 兼容客户端将大幅扩展系统的模型支持范围，使用户可以灵活选择最适合业务场景的大模型服务，同时简化新增兼容厂商的集成成本。

## What Changes

- **新增 `openai` Provider**：实现 OpenAI 标准 Chat Completions API 协议
- **扩展 LLM 工厂支持**：`llm.NewClient` 新增 "openai" / "azure" provider type
- **配置结构兼容**：支持 OpenAI 标准配置（API Key、Base URL、Model、Timeout）
- **流式响应支持预留**：接口设计考虑未来 SSE 流式输出（当前保持非流式）
- **完全向后兼容**：不影响现有 qwen/doubao provider 的使用

## Capabilities

### New Capabilities
- `openai-provider`: OpenAI API 兼容大模型 Provider 实现（支持 ChatGPT、Azure OpenAI、及所有兼容协议的开源模型）

### Modified Capabilities
- `llm-client-integration`: 扩展 factory.go 以支持新的 openai provider 类型

## Impact

**新增代码文件**：
- `internal/client/llm/openai.go` - OpenAI API 客户端实现
- `internal/client/llm/openai_test.go` - OpenAI 客户端单元测试

**修改代码文件**：
- `internal/client/llm/factory.go` - 新增 openai provider 注册
- `pkg/config/config.go` - 新增 OpenAI 相关配置字段（如已存在则无需修改）

**新增规范文档**：
- `docs/architecture/internal/client/llm/openai.md` - OpenAI Provider 架构文档
- `docs/testing/test-plans/internal/client/llm/openai.md` - OpenAI 测试计划

**配置依赖**：
需要在 config.yaml 中添加 OpenAI 配置：
```yaml
llm:
  providers:
    openai:
      api_key: "sk-xxx"
      base_url: "https://api.openai.com/v1"  # 可选，默认为 OpenAI 官方
      model: "gpt-3.5-turbo"
      timeout: "30s"
    azure:
      api_key: "xxx"
      base_url: "https://xxx.openai.azure.com"
      model: "gpt-35-turbo"
      timeout: "30s"
  routing:
    default: "openai"
    fallback_order: ["azure"]
```
