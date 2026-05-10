# WeChat Handler 配置命令架构文档

## 模块职责
- 解析微信消息中的 LLM 配置命令（如 `#设置Key`、`#模型设置` 等）
- 调用 LLMConfigService 执行配置操作
- 返回格式化的配置操作响应给用户

## 入口函数
- `handleConfigCommand(fromUserOpenID string, content string) (string, bool)`
  - 识别并分发配置命令处理
  - 返回处理结果和是否已处理标志

## 处理流程
1. 收到用户微信文本消息
2. 在 `handleTextMessage` 中调用 `handleConfigCommand` 进行命令识别
3. 根据命令前缀分发到具体处理方法：
   - `#模型设置` → `renderConfigMenu()` - 显示配置菜单
   - `#设置Key sk-xxx` → `handleSetAPIKey()` - 设置 API Key
   - `#设置地址 http://xxx` → `handleSetBaseURL()` - 设置 API 地址
   - `#我的配置` → `handleGetConfig()` - 查看当前配置
   - `#重置模型` → `handleClearConfig()` - 清除自定义配置

## 已实现能力
- ✅ 配置菜单展示
- ✅ API Key 设置（含格式验证）
- ✅ API 地址设置（含格式验证）
- ✅ 配置查看（含 API Key 脱敏）
- ✅ 配置重置清除
- ✅ 向后兼容（llmConfigService 可选参数）

## 依赖关系
- `userService.LLMConfigService` - LLM 配置服务接口
  - `SetAPIKey(userID int64, apiKey string) error`
  - `SetBaseURL(userID int64, baseURL string) error`
  - `GetConfigView(userID int64) (*LLMConfigView, error)`
  - `ClearConfig(userID int64) error`

## 注意事项
- 当前 userID 使用 0 作为占位符，未来需要集成 UserService 将 OpenID 映射到 UserID
- llmConfigService 为可选依赖，为 nil 时消息将正常走 LLM 对话流程
