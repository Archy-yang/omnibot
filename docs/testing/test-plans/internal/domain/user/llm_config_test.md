# LLMConfig 领域实体测试计划

---
## 方法：TestLLMConfig_IsEnabled

**测试目的**：验证 LLMConfig.IsEnabled() 方法正确判断配置是否启用

**输入参数**：
- APIKey: 字符串，LLM 的 API Key
- Status: int8，配置状态（0-正常，1-禁用）

**期望断言**：
- 当 APIKey 非空且 Status 为 0 时，返回 true
- 当 APIKey 为空时，无论 Status 如何，返回 false
- 当 Status 为 1（禁用）时，无论 APIKey 如何，返回 false
---

---
## 方法：TestLLMConfig_GetBaseURL

**测试目的**：验证 LLMConfig.GetBaseURL() 正确返回 API 地址，包含默认值逻辑

**输入参数**：
- BaseURL: *string，自定义 API 地址指针

**期望断言**：
- 当 BaseURL 为非空字符串指针时，返回该字符串
- 当 BaseURL 为 nil 时，返回默认值 "https://api.openai.com/v1"
- 当 BaseURL 指向空字符串时，返回默认值 "https://api.openai.com/v1"
---

---
## 方法：TestLLMConfig_GetModel

**测试目的**：验证 LLMConfig.GetModel() 正确返回模型名，包含默认值逻辑

**输入参数**：
- Model: *string，自定义模型名指针

**期望断言**：
- 当 Model 为非空字符串指针时，返回该字符串
- 当 Model 为 nil 时，返回默认值 "gpt-3.5-turbo"
- 当 Model 指向空字符串时，返回默认值 "gpt-3.5-turbo"
---
