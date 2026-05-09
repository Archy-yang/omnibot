# 测试计划：微信消息处理器 (internal/api/wechat/handler.go)

## 测试目的
验证微信公众号回调接口功能正确性，包括：
1. 签名验证功能保持正常
2. LLM 客户端正确注入到 Handler
3. 各类消息处理正确调用 LLM 并返回智能回复
4. LLM 调用失败时正确降级

## 测试环境
- 不需要外部依赖（数据库/Redis）
- 使用 Gin 框架自带的 `net/http/httptest` 进行测试
- 启动真实路由进行集成测试
- 使用 MockLLMClient 模拟大模型调用（不真实调用 API）

---

## 方法：TestHandler_Verify_ValidSignature

**测试目的**：验证当签名正确时，微信服务器接入验证通过，返回 echostr。

**输入参数**：
- HTTP Method: GET
- URL: `/wechat/callback?signature=fdf1cc36630e1abee12ce1f80ce8070e723f0fa6&timestamp=123456&nonce=abc&echostr=test_echostr`
- 配置 Token: `testtoken`

**期望断言**：
- 响应状态码: `200 OK`
- 响应内容: `test_echostr`

---

## 方法：TestHandler_Verify_InvalidSignature

**测试目的**：验证当签名错误时，返回 403 错误。

**输入参数**：
- HTTP Method: GET
- URL: `/wechat/callback?signature=wrong&timestamp=123456&nonce=abc&echostr=test_echostr`
- 配置 Token: `testtoken`

**期望断言**：
- 响应状态码: `403 Forbidden`
- 响应内容包含: `Invalid signature`

---

## 方法：TestHandler_HandleMessage_TextMessage_LLMSuccess

**测试目的**：验证用户发送文本消息，LLM 调用成功时返回 LLM 生成的回复。

**输入参数**：
- HTTP Method: POST
- URL: `/wechat/callback`
- Content-Type: `application/xml`
- 请求 Body: 文本消息 XML
- MockLLMClient 返回: `这是 LLM 生成的智能回复`

**期望断言**：
- 响应状态码: `200 OK`
- 响应内容包含: `<![CDATA[这是 LLM 生成的智能回复]]>`
- 响应内容包含正确的 ToUserName 和 FromUserName 互换

---

## 方法：TestHandler_HandleMessage_TextMessage_LLMFails

**测试目的**：验证 LLM 调用失败时，返回降级提示"服务暂时不可用，请稍后再试"。

**输入参数**：
- HTTP Method: POST
- URL: `/wechat/callback`
- Content-Type: `application/xml`
- 请求 Body: 文本消息 XML
- MockLLMClient 返回: `error: all providers failed`

**期望断言**：
- 响应状态码: `200 OK`
- 响应内容包含: `<![CDATA[服务暂时不可用，请稍后再试]]>`

---

## 方法：TestHandler_HandleMessage_ImageMessage

**测试目的**：验证用户发送图片消息时，构造正确的 prompt 调用 LLM。

**输入参数**：
- HTTP Method: POST
- URL: `/wechat/callback`
- Content-Type: `application/xml`
- 请求 Body: 图片消息 XML
- MockLLMClient 记录被调用的 prompt

**期望断言**：
- 响应状态码: `200 OK`
- MockLLMClient 被调用的 messages 中包含 user 角色的内容：`用户发送了一张图片`
- MockLLMClient 被调用的 messages 中包含 system 角色的内容：`你是一个友好的智能客服助手，请用简洁的中文回应用户的问题。`

---

## 方法：TestHandler_HandleMessage_SubscribeEvent

**测试目的**：验证用户订阅事件时，构造欢迎语 prompt 调用 LLM。

**输入参数**：
- HTTP Method: POST
- URL: `/wechat/callback`
- Content-Type: `application/xml`
- 请求 Body: subscribe 事件 XML
- MockLLMClient 记录被调用的 prompt

**期望断言**：
- 响应状态码: `200 OK`
- MockLLMClient 被调用的 messages 中包含 user 角色的内容：`用户刚刚关注了公众号，请生成友好的欢迎语`

---

## 方法：TestHandler_HandleMessage_Unsubscribe_NoResponse

**测试目的**：验证取消订阅事件不回复消息（保持原有行为）。

**输入参数**：
- HTTP Method: POST
- URL: `/wechat/callback`
- Content-Type: `application/xml`
- 请求 Body: unsubscribe 事件 XML

**期望断言**：
- 响应状态码: `200 OK`
- 响应 Body 为空字符串

---

## 已实现状态
- ✅ 测试代码已编写（7个测试用例）
- ✅ 所有测试用例通过
