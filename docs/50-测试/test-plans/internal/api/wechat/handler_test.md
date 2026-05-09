# handler_test.go 测试计划

## 方法：TestHandler_Verify_ValidSignature

**测试目的**：验证微信服务器签名验证接口在签名正确时返回 echostr

**输入参数**：
- 正确的 signature、timestamp、nonce、echostr 参数

**期望断言**：
- HTTP 状态码为 200
- 返回内容为传入的 echostr
- 不应该调用 LLM

---

## 方法：TestHandler_Verify_InvalidSignature

**测试目的**：验证微信服务器签名验证接口在签名错误时返回 403

**输入参数**：
- 错误的 signature 参数

**期望断言**：
- HTTP 状态码为 403
- 返回内容包含 "Invalid signature"

---

## 方法：TestHandler_HandleMessage_TextMessage_LLMSuccess

**测试目的**：验证文本消息处理在 LLM 调用成功时返回正确响应

**输入参数**：
- 文本消息 XML
- Mock LLM 返回成功响应

**期望断言**：
- HTTP 状态码为 200
- 应该调用 LLM
- 响应内容包含 LLM 生成的回复
- 应该调用 UserService.GetOrCreateByOpenID

---

## 方法：TestHandler_HandleMessage_TextMessage_LLMFails

**测试目的**：验证文本消息处理在 LLM 调用失败时返回降级提示

**输入参数**：
- 文本消息 XML
- Mock LLM 返回错误

**期望断言**：
- HTTP 状态码为 200
- 应该调用 LLM
- 响应内容包含 "服务暂时不可用，请稍后再试"
- 应该调用 UserService.GetOrCreateByOpenID

---

## 方法：TestHandler_HandleMessage_ImageMessage

**测试目的**：验证图片消息处理正确调用 LLM 并返回响应

**输入参数**：
- 图片消息 XML
- Mock LLM 返回成功响应

**期望断言**：
- HTTP 状态码为 200
- 应该调用 LLM
- LLM 调用包含两条消息（system 和 user）
- system 消息内容为预设提示词
- user 消息内容为 "用户发送了一张图片"

---

## 方法：TestHandler_HandleMessage_SubscribeEvent_CreatesUser

**测试目的**：验证关注事件时创建新用户并返回欢迎语

**输入参数**：
- subscribe 事件消息 XML
- Mock LLM 返回欢迎语
- Mock UserService 标记为新用户

**期望断言**：
- HTTP 状态码为 200
- 应该调用 LLM
- 应该调用 UserService.GetOrCreateByOpenID
- 响应内容包含欢迎语

---

## 方法：TestHandler_HandleMessage_SubscribeEvent_UserServiceError

**测试目的**：验证 UserService 出错时不影响消息正常处理

**输入参数**：
- subscribe 事件消息 XML
- Mock UserService 返回错误

**期望断言**：
- HTTP 状态码为 200
- 应该调用 UserService.GetOrCreateByOpenID
- 仍然调用 LLM 生成欢迎语
- 响应内容包含欢迎语

---

## 方法：TestHandler_HandleMessage_Unsubscribe_NoResponse

**测试目的**：验证取消订阅事件不调用 LLM 且不返回消息

**输入参数**：
- unsubscribe 事件消息 XML

**期望断言**：
- HTTP 状态码为 200
- 不应该调用 LLM
- 响应内容为空字符串
