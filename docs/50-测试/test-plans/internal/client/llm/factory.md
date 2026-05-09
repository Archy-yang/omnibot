# 测试计划：internal/client/llm/factory.go

## 测试目的
验证工厂根据配置正确创建 LLM 客户端，支持默认厂商和降级顺序。

## 测试环境
- 不需要外部依赖，不实际调用 API
- 使用配置模拟创建

---

## 方法：TestFactory_CreateClient_Success

**测试目的**：当配置正确时，成功创建客户端。

**输入参数**：
- 配置：providers 包含 qwen 和 doubao，default = qwen，fallback = [qwen, doubao]

**期望断言**：
- 创建客户端成功，不返回错误
- 默认 provider 是 qwen

---

## 方法：TestFactory_CreateClient_NotFoundDefault

**测试目的**：默认厂商在配置中不存在，返回错误。

**输入参数**：
- 配置：providers 包含 qwen，default = doubao（不存在）

**期望断言**：
- 返回错误，错误信息包含 "default provider not found"
- 返回 nil 客户端

---

## 方法：TestClient_ChatCompletion_Fallback

**测试目的**：默认厂商失败，自动尝试 fallback 厂商。

**输入参数**：
- mock 默认厂商：总是返回错误
- mock fallback 厂商：成功返回 "fallback response"

**期望断言**：
- 最终返回成功
- 返回内容是 fallback 厂商的回复

---

## 方法：TestClient_ChatCompletion_AllFailed

**测试目的**：默认和所有 fallback 都失败，返回最终错误。

**输入参数**：
- mock 默认厂商：失败
- mock 所有 fallback 厂商：都失败

**期望断言**：
- 返回错误
- 错误信息说明所有厂商都失败

---

## 已实现状态
- ✅ 测试代码已编写
- ✅ 所有测试用例通过
