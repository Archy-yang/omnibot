# WeChat Handler 配置命令测试计划

## 概述
测试微信 Handler 中 LLM 配置命令的处理逻辑，确保配置命令能正确解析和调用服务。

---
## 方法：TestHandler_ConfigCommands_SetAPIKey

**测试目的**：验证 `#设置Key` 命令能正确调用 LLMConfigService 设置 API Key

**输入参数**：
- 用户 OpenID: "user123"
- 命令内容: "#设置Key sk-test-api-key-12345678901234567890"

**期望断言**：
- handled 为 true
- 回复内容包含 "API Key 设置成功"
- 调用 GetConfigForUser 能查询到已保存的配置
- hasCustomConfig 为 true

---
## 方法：TestHandler_ConfigCommands_ConfigMenu

**测试目的**：验证 `#模型设置` 命令能返回配置菜单

**输入参数**：
- 用户 OpenID: "user123"
- 命令内容: "#模型设置"

**期望断言**：
- handled 为 true
- 回复内容包含 "模型设置"
- 回复内容包含 "设置 API Key"
- 回复内容包含 "设置 API 地址"

---
## 方法：TestHandler_ConfigCommands_GetConfigView

**测试目的**：验证 `#我的配置` 命令能返回当前配置视图

**输入参数**：
- 用户 OpenID: "user123"
- 命令内容: "#我的配置" (先设置 API Key)

**期望断言**：
- handled 为 true
- 回复内容包含 "当前配置"
- 回复内容包含 "..." (脱敏标识)

---
## 方法：TestHandler_ConfigCommands_ClearConfig

**测试目的**：验证 `#重置模型` 命令能清除用户配置

**输入参数**：
- 用户 OpenID: "user123"
- 命令内容: "#重置模型" (先设置 API Key)

**期望断言**：
- handled 为 true
- 回复内容包含 "已重置为系统默认模型"
