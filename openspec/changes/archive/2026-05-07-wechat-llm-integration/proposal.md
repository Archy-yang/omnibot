## Why

大模型客户端已开发完成（支持通义千问 + 字节豆包双厂商自动降级），现在需要将微信消息处理器与大模型客户端集成。当前微信公众号所有消息都返回固定占位文本"客服在开发中，敬请期待"，无法提供真正的智能对话服务。

集成后，用户发送的所有消息类型（文本、图片、语音、订阅事件等）都将触发大模型调用，返回真正的智能回复，完成微信公众号智能对话客服系统的核心功能闭环。

## What Changes

- **wechat.Handler 结构体新增 llm.Client 指针字段**
- **NewHandler 构造函数新增 llm.Client 参数**（**BREAKING**: 所有调用处需要更新）
- **routes.SetupRouter 创建处理器时注入配置好的 llm.Client**
- **所有消息处理函数（文本/图片/语音/事件等）改为调用大模型生成回复**
- **新增对话消息构造逻辑：根据消息类型构造不同的 user role 提示词**
- **新增系统提示词："你是一个友好的智能客服助手，请用简洁的中文回应用户的问题。"**
- **新增错误处理：大模型调用失败时返回"服务暂时不可用，请稍后再试"**

## Capabilities

### Modified Capabilities
- **wechat-callback-api**: 新增 LLM 集成相关的需求，包括依赖注入、消息处理流程变更、错误降级处理
- **llm-client-integration**: 新增与微信处理器集成的调用场景说明

## Impact

**受影响代码文件：**
- `internal/api/wechat/handler.go`: Handler 结构体定义、NewHandler 签名、所有消息处理函数
- `internal/api/routes.go`: SetupRouter 函数中处理器创建逻辑

**受影响测试：**
- `internal/api/wechat/handler_test.go`: 所有现有测试需要更新，新增 mock LLM 客户端

**依赖：**
- 完全依赖已完成的 `internal/client/llm` 包，无需新增外部依赖
