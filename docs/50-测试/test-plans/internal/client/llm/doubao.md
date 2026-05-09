# 测试计划：internal/client/llm/doubao.go

## 测试目的
验证字节豆包客户端正确转换请求格式和解析响应。

## 测试环境
- 不实际调用 API，使用 mock HTTP 客户端

---

## 方法：TestDoubao_ConvertMessages

**测试目的**：内部 ChatMessage 正确转换为豆包要求的格式。

**输入参数**：
- messages:
  ```
  [
    {Role: "system", Content: "你是一个助手"},
    {Role: "user", Content: "你好"},
  ]
  ```

**期望断言**：
- 转换后的格式符合豆包 API 要求
- 角色正确映射

---

## 方法：TestDoubao_ParseResponse

**测试目的**：正确解析豆包 API 响应，提取回复文本。

**输入参数**：
- 模拟 JSON 响应，包含 response.content

**期望断言**：
- 正确提取回复文本
- 不返回错误

---

## 方法：TestDoubao_ParseError

**测试目的**：API 返回错误时，正确返回错误信息。

**输入参数**：
- 模拟 JSON 响应，包含错误信息

**期望断言**：
- 返回 error
- 错误信息包含 API 返回的错误内容

---

## 已实现状态
- ✅ 功能实现完成
- ⬜ 单元测试待补充（需要 mock HTTP client，不实际调用 API）
