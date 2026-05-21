## 1. Handler 结构体与构造函数修改

- [x] 1.1 修改 wechat.Handler 结构体，新增 llmClient 字段（*llm.Client 类型）
- [x] 1.2 修改 NewHandler 函数签名，新增 *llm.Client 参数
- [x] 1.3 更新 imports，添加 llm 包引用

## 2. 路由依赖注入

- [x] 2.1 修改 routes.SetupRouter 函数，在创建 wechat.Handler 前先创建 llm.Client
- [x] 2.2 处理 llm.NewClient 可能返回的错误（配置错误时 panic 或日志告警）
- [x] 2.3 将创建好的 llm.Client 注入到 wechat.NewHandler

## 3. 消息处理核心逻辑

- [x] 3.1 定义系统提示词常量："你是一个友好的智能客服助手，请用简洁的中文回应用户的问题。"
- [x] 3.2 修改 handleTextMessage 函数：构造 [system, user] 消息数组，调用 llmClient.ChatCompletion
- [x] 3.3 修改 handleImageMessage 函数：构造 "用户发送了一张图片" 作为 user 消息
- [x] 3.4 修改 handleVoiceMessage 函数：构造 "用户发送了一条语音消息" 作为 user 消息
- [x] 3.5 修改 handleVideoMessage / handleShortVideoMessage：构造视频消息提示词
- [x] 3.6 修改 handleLocationMessage：构造位置消息提示词
- [x] 3.7 修改 handleLinkMessage：构造链接消息提示词
- [x] 3.8 修改 handleSubscribeEvent：构造 "用户刚刚关注了公众号，请生成友好的欢迎语"

## 4. 错误处理与降级

- [x] 4.1 在所有调用大模型的函数中处理 error：失败时返回 "服务暂时不可用，请稍后再试"
- [x] 4.2 添加大模型调用成功/失败的日志记录（包含消息类型、耗时等信息）
- [x] 4.3 确保取消订阅和 VIEW 事件仍然保持不回复的行为

## 5. 单元测试

- [x] 5.1 创建 MockLLMClient 实现（或使用接口抽象），用于单元测试
- [x] 5.2 更新 TestHandler_Verify_* 测试：传入 mock LLM client
- [x] 5.3 新增 TestHandler_HandleMessage_TextMessage_LLMSuccess：验证文本消息调用大模型成功
- [x] 5.4 新增 TestHandler_HandleMessage_TextMessage_LLMFails：验证大模型失败时的降级提示
- [x] 5.5 新增 TestHandler_HandleMessage_ImageMessage：验证图片消息调用大模型
- [x] 5.6 新增 TestHandler_HandleMessage_SubscribeEvent：验证订阅事件生成欢迎语
- [x] 5.7 确保所有测试通过

## 6. 文档更新

- [x] 6.1 更新架构文档 docs/architecture/internal/api/wechat/handler.md
- [x] 6.2 更新测试计划文档 docs/testing/test-plans/internal/api/wechat/handler.md
