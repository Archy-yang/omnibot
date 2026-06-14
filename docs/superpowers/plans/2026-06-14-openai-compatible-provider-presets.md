# OpenAI 兼容服务商预设配置 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build v1.4.1 Web LLM configuration presets so users can choose OpenAI-compatible providers for OpenAI 官方、百度千帆、字节火山、阿里千问, while native provider options remain visible but disabled.

**Architecture:** Keep one user LLM config per user and route all new preset IDs through the existing `OpenAIProvider`. Add a backend provider preset registry and a read-only provider-options API so the frontend renders available/disabled providers from server-owned metadata. Preserve legacy `qwen`/`doubao` behavior for existing configs, but do not let the Web UI save native provider IDs in v1.4.1.

**Tech Stack:** Go 1.24 + Gin + GORM + SQLite tests; Vue 3 + TypeScript + Pinia + Naive UI + Tailwind CSS; existing AES-256-GCM API Key encryption.

---

## Scope and source documents

- Product PRD: `docs/20-产品PRD/in_progress/v1.4.1-OpenAI兼容服务商预设配置PRD.md`
- Backend config service: `internal/service/user/llm_config_service.go`
- Backend domain defaults: `internal/domain/user/llm_config.go`
- LLM factory: `internal/client/llm/factory.go`
- Web API handler: `internal/api/web/handler.go`
- Routes: `internal/api/routes.go`
- Frontend settings panel: `frontend/src/components/functional/SettingsPanel.vue`
- Frontend settings store: `frontend/src/stores/settings.ts`
- Frontend API service/types: `frontend/src/services/config.ts`, `frontend/src/types/api.ts`
- Related architecture docs: `docs/30-服务架构/02-模块设计/用户域/llm-config-service.md`, `docs/30-服务架构/02-模块设计/基础设施层/llm-client.md`

## File structure changes

### Create

- `docs/50-测试/test-plans/v1.4.1-openai-compatible-provider-presets.md`
  - Test plan required before implementation by the project TDD workflow.
- `internal/service/user/llm_provider_preset.go`
  - Backend-owned provider preset registry and validation helpers.
- `frontend/src/types/llmProvider.ts`
  - Frontend provider option types returned by backend API.

### Modify

- `internal/domain/user/llm_config.go`
  - Add default Base URL/model cases for new OpenAI-compatible preset IDs.
  - Keep legacy `qwen` and `doubao` defaults readable for existing data.
- `internal/service/user/llm_config_service.go`
  - Expose provider options via service interface.
  - Validate provider status and required Base URL/model rules.
  - Preserve existing encrypted API Key when editing config with empty API Key.
- `internal/service/user/llm_config_service_test.go`
  - Add TDD coverage for presets, disabled native providers, custom provider validation, and edit-without-key behavior.
- `internal/client/llm/factory.go`
  - Route new preset IDs (`baidu_qianfan`, `volcengine`, `aliyun_qwen`, `custom_openai_compatible`, `openai`) through `OpenAIProvider`.
  - Preserve legacy native provider routing for `qwen` and `doubao`.
- `internal/client/llm/factory_test.go`
  - Add direct tests that user-config preset IDs create an OpenAI-compatible client.
- `internal/api/web/handler.go`
  - Add `HandleGetLLMProviders` endpoint and response DTOs.
  - Keep `HandleUpdateLLMConfig` passing provider ID, base URL, model, temperature, max tokens.
- `internal/api/web/handler_test.go`
  - Add provider-options endpoint tests.
  - Add update tests for a new OpenAI-compatible preset and disabled native provider failure.
- `internal/api/routes.go`
  - Register `GET /api/v1/user/llm-providers`.
- `frontend/src/types/api.ts`
  - Reference provider option response types and keep user LLM config request/response fields typed.
- `frontend/src/services/config.ts`
  - Add `getUserLLMProviders()`.
- `frontend/src/stores/settings.ts`
  - Load provider options from backend.
  - Preserve default OpenAI-compatible values.
- `frontend/src/components/functional/SettingsPanel.vue`
  - Render grouped available/disabled providers.
  - Auto-fill Base URL/model for presets.
  - Disable save for native provider selections and show guidance.
- `docs/30-服务架构/02-模块设计/用户域/llm-config-service.md`
  - Document v1.4.1 provider preset registry and disabled native provider policy.
- `docs/30-服务架构/02-模块设计/基础设施层/llm-client.md`
  - Document new user-config preset IDs and routing through `OpenAIProvider`.
- `docs/90-迭代记录/CHANGELOG.md`
  - Add an unreleased v1.4.1 entry after implementation verification.

---

## Task 1: Add test plan document

**Files:**
- Create: `docs/50-测试/test-plans/v1.4.1-openai-compatible-provider-presets.md`

- [ ] **Step 1: Create the test plan before code changes**

Write this content:

```markdown
# v1.4.1 OpenAI 兼容服务商预设配置测试计划

## 一、测试目标

验证 Web 用户级 LLM 配置支持 OpenAI-compatible 服务商预设：OpenAI 官方、百度千帆、字节火山、阿里千问、自定义 OpenAI-compatible；验证专用 Provider 展示但不可保存；验证保存后同步聊天和 SSE 流式聊天走用户 OpenAI-compatible 配置。

## 二、测试范围

| 范围 | 覆盖 |
|------|------|
| 后端服务层 | provider 预设列表、provider 校验、默认 Base URL / 模型、编辑时保留旧 API Key |
| LLM 客户端工厂 | 新 preset ID 走 OpenAIProvider； legacy qwen/doubao 保留专用 Provider |
| Web API | provider-options API、配置保存 API、禁用专用 Provider 错误 |
| 前端设置面板 | 分组展示、禁用状态、预设自动填充、保存 payload |
| 运行验证 | 后端 API smoke、前端页面可访问、配置保存后聊天调用路径 |

## 三、后端测试用例

| 用例 | 期望 |
|------|------|
| ListProviderOptions 返回可用和禁用分组 | 包含 openai/baidu_qianfan/volcengine/aliyun_qwen/custom_openai_compatible 可用项，包含 qianfan_native/qwen_native/doubao_native 禁用项 |
| 保存 baidu_qianfan 且不传 BaseURL/Model | 使用千帆默认 Base URL 和推荐模型 |
| 保存 volcengine 且不传 BaseURL/Model | 使用火山默认 Base URL 和推荐模型 |
| 保存 aliyun_qwen 且不传 BaseURL/Model | 使用阿里千问默认 Base URL 和推荐模型 |
| 保存 custom_openai_compatible 但 BaseURL 为空 | 返回“请输入 API 地址” |
| 保存 qwen_native/doubao_native/qianfan_native | 返回“专用接口暂不可用，请使用 OpenAI 兼容模式” |
| 编辑已有配置时 API Key 为空 | 保留原 API Key，只更新 provider/baseURL/model/temperature/maxTokens |
| NewClientFromUserConfig 使用 baidu_qianfan/volcengine/aliyun_qwen | 创建 OpenAIProvider 客户端 |
| NewClientFromUserConfig 使用 legacy qwen/doubao | 仍创建专用 Provider 客户端 |

## 四、前端验证用例

| 用例 | 期望 |
|------|------|
| 打开设置面板 | 先加载 provider options，再展示 LLM 配置表单 |
| 选择百度千帆 | Base URL 自动为 `https://qianfan.baidubce.com/v2`，模型为 `ernie-4.0-turbo-8k` |
| 选择字节火山 | Base URL 自动为 `https://ark.cn-beijing.volces.com/api/v3`，模型为 `doubao-seed-1-6` |
| 选择阿里千问 | Base URL 自动为 `https://dashscope.aliyuncs.com/compatible-mode/v1`，模型为 `qwen-plus` |
| 选择自定义 OpenAI-compatible | Base URL 和模型可手动填写 |
| 选择专用接口禁用项 | 保存按钮不可用或保存前提示专用接口暂不可用 |

## 五、执行命令

```bash
go test ./internal/service/user ./internal/client/llm ./internal/api/web
npm --prefix frontend run build
```

## 六、手工 smoke 验证

1. 启动后端：`go run cmd/server/main.go -config configs/config.yaml`
2. 启动前端：`npm --prefix frontend run dev -- --host 127.0.0.1`
3. 访问：`http://127.0.0.1:5173/`
4. 打开设置面板，选择 OpenAI 兼容预设，保存配置。
5. 调用 `GET /api/v1/user/llm-config?session_id=<session>`，确认 provider/base_url/model 已保存。
```

- [ ] **Step 2: Review the test plan**

Confirm the file contains no placeholder tokens and covers backend, frontend, and runtime smoke verification.

Run only if commit authorization has been explicitly granted:

```bash
git add docs/50-测试/test-plans/v1.4.1-openai-compatible-provider-presets.md
git commit -m "test(llm-config): add provider preset test plan"
```

Commit body, when committing, must include:

```text
Co-Authored-By: Claude <noreply@anthropic.com>
```

---

## Task 2: Add backend provider preset registry and service tests

**Files:**
- Create: `internal/service/user/llm_provider_preset.go`
- Modify: `internal/service/user/llm_config_service.go`
- Modify: `internal/service/user/llm_config_service_test.go`

- [ ] **Step 1: Write failing service tests**

Append these tests to `internal/service/user/llm_config_service_test.go`:

```go
func TestLLMConfigService_ListProviderOptions(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	options := service.ListProviderOptions()

	assert.Len(t, options, 8)
	assert.Equal(t, "openai", options[0].Value)
	assert.Equal(t, "OpenAI 官方", options[0].Label)
	assert.Equal(t, "openai_compatible", options[0].Mode)
	assert.Equal(t, "available", options[0].Status)
	assert.Equal(t, "https://api.openai.com/v1", options[0].DefaultBaseURL)
	assert.Equal(t, "gpt-4o-mini", options[0].DefaultModel)

	assert.Equal(t, "baidu_qianfan", options[1].Value)
	assert.Equal(t, "百度千帆", options[1].Label)
	assert.Equal(t, "https://qianfan.baidubce.com/v2", options[1].DefaultBaseURL)
	assert.Equal(t, "ernie-4.0-turbo-8k", options[1].DefaultModel)

	assert.Equal(t, "volcengine", options[2].Value)
	assert.Equal(t, "字节火山", options[2].Label)
	assert.Equal(t, "https://ark.cn-beijing.volces.com/api/v3", options[2].DefaultBaseURL)
	assert.Equal(t, "doubao-seed-1-6", options[2].DefaultModel)

	assert.Equal(t, "aliyun_qwen", options[3].Value)
	assert.Equal(t, "阿里千问", options[3].Label)
	assert.Equal(t, "https://dashscope.aliyuncs.com/compatible-mode/v1", options[3].DefaultBaseURL)
	assert.Equal(t, "qwen-plus", options[3].DefaultModel)

	assert.Equal(t, "custom_openai_compatible", options[4].Value)
	assert.Equal(t, "自定义 OpenAI-compatible", options[4].Label)
	assert.Equal(t, "", options[4].DefaultBaseURL)
	assert.Equal(t, "", options[4].DefaultModel)

	assert.Equal(t, "qianfan_native", options[5].Value)
	assert.Equal(t, "disabled", options[5].Status)
	assert.Equal(t, "专用接口暂不可用，请使用 OpenAI 兼容模式。", options[5].DisabledReason)
}

func TestLLMConfigService_UpdateFullConfig_OpenAICompatiblePresetDefaults(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	cases := []struct {
		name        string
		provider    string
		baseURL     string
		model       string
	}{
		{
			name:     "baidu qianfan",
			provider: "baidu_qianfan",
			baseURL:  "https://qianfan.baidubce.com/v2",
			model:    "ernie-4.0-turbo-8k",
		},
		{
			name:     "volcengine",
			provider: "volcengine",
			baseURL:  "https://ark.cn-beijing.volces.com/api/v3",
			model:    "doubao-seed-1-6",
		},
		{
			name:     "aliyun qwen",
			provider: "aliyun_qwen",
			baseURL:  "https://dashscope.aliyuncs.com/compatible-mode/v1",
			model:    "qwen-plus",
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userID := int64(i + 1)
			err := service.UpdateFullConfig(userID, UpdateConfigRequest{
				Provider:    tc.provider,
				APIKey:      "sk-test-key-1234567890abcdefghijklmnop",
				Temperature: 0.7,
				MaxTokens:   2048,
			})
			require.NoError(t, err)

			config, hasConfig, err := service.GetFullConfigForUser(userID)
			require.NoError(t, err)
			assert.True(t, hasConfig)
			assert.Equal(t, tc.provider, config.Provider)
			assert.Equal(t, tc.baseURL, config.BaseURL)
			assert.Equal(t, tc.model, config.Model)
		})
	}
}

func TestLLMConfigService_UpdateFullConfig_CustomOpenAICompatibleRequiresBaseURLAndModel(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	err := service.UpdateFullConfig(1, UpdateConfigRequest{
		Provider:    "custom_openai_compatible",
		APIKey:      "sk-test-key-1234567890abcdefghijklmnop",
		Model:       "custom-model",
		Temperature: 0.7,
		MaxTokens:   2048,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "请输入 API 地址")

	err = service.UpdateFullConfig(1, UpdateConfigRequest{
		Provider:    "custom_openai_compatible",
		APIKey:      "sk-test-key-1234567890abcdefghijklmnop",
		BaseURL:     "https://models.example.com/v1",
		Temperature: 0.7,
		MaxTokens:   2048,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "请输入模型名称")
}

func TestLLMConfigService_UpdateFullConfig_NativeProvidersDisabled(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	for _, provider := range []string{"qianfan_native", "qwen_native", "doubao_native"} {
		t.Run(provider, func(t *testing.T) {
			err := service.UpdateFullConfig(1, UpdateConfigRequest{
				Provider:    provider,
				APIKey:      "sk-test-key-1234567890abcdefghijklmnop",
				BaseURL:     "https://example.com/v1",
				Model:       "native-model",
				Temperature: 0.7,
				MaxTokens:   2048,
			})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "专用接口暂不可用")
		})
	}
}

func TestLLMConfigService_UpdateFullConfig_KeepExistingAPIKeyWhenEmpty(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	err := service.UpdateFullConfig(1, UpdateConfigRequest{
		Provider:    "openai",
		APIKey:      "sk-original-key-1234567890abcdefghijklm",
		BaseURL:     "https://api.openai.com/v1",
		Model:       "gpt-4o-mini",
		Temperature: 0.7,
		MaxTokens:   2048,
	})
	require.NoError(t, err)

	err = service.UpdateFullConfig(1, UpdateConfigRequest{
		Provider:    "aliyun_qwen",
		APIKey:      "",
		BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Model:       "qwen-plus",
		Temperature: 0.5,
		MaxTokens:   4096,
	})
	require.NoError(t, err)

	config, hasConfig, err := service.GetFullConfigForUser(1)
	require.NoError(t, err)
	assert.True(t, hasConfig)
	assert.Equal(t, "sk-original-key-1234567890abcdefghijklm", config.APIKey)
	assert.Equal(t, "aliyun_qwen", config.Provider)
	assert.Equal(t, "qwen-plus", config.Model)
}
```

- [ ] **Step 2: Run service tests and verify RED**

Run:

```bash
go test ./internal/service/user -run 'TestLLMConfigService_(ListProviderOptions|UpdateFullConfig_OpenAICompatiblePresetDefaults|UpdateFullConfig_CustomOpenAICompatibleRequiresBaseURLAndModel|UpdateFullConfig_NativeProvidersDisabled|UpdateFullConfig_KeepExistingAPIKeyWhenEmpty)' -count=1
```

Expected: FAIL because `ListProviderOptions`, `ProviderOption`, and new validation rules do not exist yet.

- [ ] **Step 3: Add provider preset registry**

Create `internal/service/user/llm_provider_preset.go`:

```go
package user

const (
	ProviderModeOpenAICompatible = "openai_compatible"
	ProviderModeNative           = "native"

	ProviderStatusAvailable = "available"
	ProviderStatusDisabled  = "disabled"
)

const nativeProviderDisabledReason = "专用接口暂不可用，请使用 OpenAI 兼容模式。"

type ProviderOption struct {
	Value          string
	Label          string
	Mode           string
	Status         string
	DefaultBaseURL string
	DefaultModel   string
	Description    string
	DisabledReason string
}

var providerOptions = []ProviderOption{
	{
		Value:          "openai",
		Label:          "OpenAI 官方",
		Mode:           ProviderModeOpenAICompatible,
		Status:         ProviderStatusAvailable,
		DefaultBaseURL: "https://api.openai.com/v1",
		DefaultModel:   "gpt-4o-mini",
		Description:    "OpenAI 官方 Chat Completions API。",
	},
	{
		Value:          "baidu_qianfan",
		Label:          "百度千帆",
		Mode:           ProviderModeOpenAICompatible,
		Status:         ProviderStatusAvailable,
		DefaultBaseURL: "https://qianfan.baidubce.com/v2",
		DefaultModel:   "ernie-4.0-turbo-8k",
		Description:    "百度智能云千帆 OpenAI 兼容模式。",
	},
	{
		Value:          "volcengine",
		Label:          "字节火山",
		Mode:           ProviderModeOpenAICompatible,
		Status:         ProviderStatusAvailable,
		DefaultBaseURL: "https://ark.cn-beijing.volces.com/api/v3",
		DefaultModel:   "doubao-seed-1-6",
		Description:    "火山方舟 OpenAI 兼容模式。",
	},
	{
		Value:          "aliyun_qwen",
		Label:          "阿里千问",
		Mode:           ProviderModeOpenAICompatible,
		Status:         ProviderStatusAvailable,
		DefaultBaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		DefaultModel:   "qwen-plus",
		Description:    "阿里云 DashScope OpenAI 兼容模式。",
	},
	{
		Value:       "custom_openai_compatible",
		Label:       "自定义 OpenAI-compatible",
		Mode:        ProviderModeOpenAICompatible,
		Status:      ProviderStatusAvailable,
		Description: "适用于 One API、New API、vLLM、Ollama 网关或其他 OpenAI 兼容服务。",
	},
	{
		Value:          "qianfan_native",
		Label:          "百度千帆专用",
		Mode:           ProviderModeNative,
		Status:         ProviderStatusDisabled,
		Description:    "百度千帆原生接口。",
		DisabledReason: nativeProviderDisabledReason,
	},
	{
		Value:          "qwen_native",
		Label:          "通义千问专用",
		Mode:           ProviderModeNative,
		Status:         ProviderStatusDisabled,
		Description:    "通义千问 DashScope 原生接口。当前请使用 OpenAI 兼容 → 阿里千问。",
		DisabledReason: nativeProviderDisabledReason,
	},
	{
		Value:          "doubao_native",
		Label:          "豆包专用",
		Mode:           ProviderModeNative,
		Status:         ProviderStatusDisabled,
		Description:    "火山方舟豆包原生接口。当前请使用 OpenAI 兼容 → 字节火山。",
		DisabledReason: nativeProviderDisabledReason,
	},
}

func listProviderOptions() []ProviderOption {
	items := make([]ProviderOption, len(providerOptions))
	copy(items, providerOptions)
	return items
}

func findProviderOption(value string) (ProviderOption, bool) {
	for _, item := range providerOptions {
		if item.Value == value {
			return item, true
		}
	}
	return ProviderOption{}, false
}

func isLegacyProvider(value string) bool {
	switch value {
	case "qwen", "doubao", "anthropic", "azure":
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: Extend service interface and validation**

In `internal/service/user/llm_config_service.go`, add this method to `LLMConfigService`:

```go
ListProviderOptions() []ProviderOption
```

Add this method on `GormLLMConfigService` near `NewLLMConfigService`:

```go
func (s *GormLLMConfigService) ListProviderOptions() []ProviderOption {
	return listProviderOptions()
}
```

Replace the provider validation block in `UpdateFullConfig` with:

```go
	providerOption, knownProvider := findProviderOption(req.Provider)
	if !knownProvider && !isLegacyProvider(req.Provider) {
		return errors.New("不支持的服务商")
	}
	if knownProvider && providerOption.Status == ProviderStatusDisabled {
		return errors.New(nativeProviderDisabledReason)
	}
```

After URL format validation, add required fields for custom OpenAI-compatible:

```go
	if req.Provider == "custom_openai_compatible" {
		if req.BaseURL == "" {
			return errors.New("请输入 API 地址")
		}
		if req.Model == "" {
			return errors.New("请输入模型名称")
		}
	}
```

When creating a new config, after setting `Provider` and `APIKey`, fill defaults from `providerOption`:

```go
			baseURL := req.BaseURL
			if baseURL == "" && knownProvider {
				baseURL = providerOption.DefaultBaseURL
			}
			if baseURL != "" {
				cfg.BaseURL = &baseURL
			}

			model := req.Model
			if model == "" && knownProvider {
				model = providerOption.DefaultModel
			}
			if model != "" {
				cfg.Model = &model
			}
```

When updating an existing config, replace the current BaseURL/Model update block with:

```go
		baseURL := req.BaseURL
		if baseURL == "" && knownProvider {
			baseURL = providerOption.DefaultBaseURL
		}
		if baseURL != "" {
			cfg.BaseURL = &baseURL
		}

		model := req.Model
		if model == "" && knownProvider {
			model = providerOption.DefaultModel
		}
		if model != "" {
			cfg.Model = &model
		}
```

Keep this existing behavior unchanged:

```go
		if req.APIKey != "" {
			cfg.APIKey = encryptedKey
		}
```

- [ ] **Step 5: Run service tests and verify GREEN**

Run:

```bash
go test ./internal/service/user -count=1
```

Expected: PASS.

Run only if commit authorization has been explicitly granted:

```bash
git add internal/service/user/llm_provider_preset.go internal/service/user/llm_config_service.go internal/service/user/llm_config_service_test.go
git commit -m "feat(llm-config): add provider preset registry"
```

Commit body, when committing, must include:

```text
Co-Authored-By: Claude <noreply@anthropic.com>
```

---

## Task 3: Add domain defaults and user-config LLM factory routing

**Files:**
- Modify: `internal/domain/user/llm_config.go`
- Modify: `internal/client/llm/factory.go`
- Modify: `internal/client/llm/factory_test.go`

- [ ] **Step 1: Write failing factory tests**

Append this test to `internal/client/llm/factory_test.go`:

```go
func TestNewClientFromUserConfig_OpenAICompatiblePresets(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		baseURL  string
		model    string
	}{
		{name: "openai", provider: "openai", baseURL: "https://api.openai.com/v1", model: "gpt-4o-mini"},
		{name: "baidu qianfan", provider: "baidu_qianfan", baseURL: "https://qianfan.baidubce.com/v2", model: "ernie-4.0-turbo-8k"},
		{name: "volcengine", provider: "volcengine", baseURL: "https://ark.cn-beijing.volces.com/api/v3", model: "doubao-seed-1-6"},
		{name: "aliyun qwen", provider: "aliyun_qwen", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", model: "qwen-plus"},
		{name: "custom", provider: "custom_openai_compatible", baseURL: "https://models.example.com/v1", model: "custom-model"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClientFromUserConfig(UserConfig{
				Provider: tc.provider,
				APIKey:   "sk-test-key-1234567890abcdefghijklmnop",
				BaseURL:  tc.baseURL,
				Model:    tc.model,
			})
			assert.NoError(t, err)
			assert.NotNil(t, client)
			_, ok := client.defaultProvider.(*OpenAIProvider)
			assert.True(t, ok, "provider %s should use OpenAIProvider", tc.provider)
		})
	}
}

func TestNewClientFromUserConfig_LegacyNativeProvidersStillSupported(t *testing.T) {
	qwenClient, err := NewClientFromUserConfig(UserConfig{
		Provider: "qwen",
		APIKey:   "sk-test-key-1234567890abcdefghijklmnop",
		Model:    "qwen-turbo",
	})
	require.NoError(t, err)
	_, isQwen := qwenClient.defaultProvider.(*QwenProvider)
	assert.True(t, isQwen)

	doubaoClient, err := NewClientFromUserConfig(UserConfig{
		Provider: "doubao",
		APIKey:   "sk-test-key-1234567890abcdefghijklmnop",
		Model:    "doubao-pro-32k",
	})
	require.NoError(t, err)
	_, isDoubao := doubaoClient.defaultProvider.(*DoubaoProvider)
	assert.True(t, isDoubao)
}
```

Update imports in `factory_test.go` to include `require`:

```go
	"github.com/stretchr/testify/require"
```

- [ ] **Step 2: Run factory tests and verify RED**

Run:

```bash
go test ./internal/client/llm -run 'TestNewClientFromUserConfig' -count=1
```

Expected: FAIL because the new provider IDs are not supported by `NewClientFromUserConfig`.

- [ ] **Step 3: Add domain defaults for new preset IDs**

In `internal/domain/user/llm_config.go`, update `getDefaultBaseURL()` switch:

```go
	switch c.Provider {
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "qwen", "aliyun_qwen":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case "doubao", "volcengine":
		return "https://ark.cn-beijing.volces.com/api/v3"
	case "baidu_qianfan":
		return "https://qianfan.baidubce.com/v2"
	case "azure", "custom_openai_compatible":
		return ""
	default:
		return "https://api.openai.com/v1"
	}
```

Update `getDefaultModel()` switch:

```go
	switch c.Provider {
	case "anthropic":
		return "claude-3-sonnet-20240229"
	case "qwen":
		return "qwen-turbo"
	case "aliyun_qwen":
		return "qwen-plus"
	case "doubao":
		return "doubao-pro-32k"
	case "volcengine":
		return "doubao-seed-1-6"
	case "baidu_qianfan":
		return "ernie-4.0-turbo-8k"
	case "azure", "custom_openai_compatible":
		return ""
	default:
		return "gpt-4o-mini"
	}
```

- [ ] **Step 4: Route new preset IDs to OpenAIProvider**

In `internal/client/llm/factory.go`, update the `UserConfig` comment:

```go
	Provider string // 服务商：openai/baidu_qianfan/volcengine/aliyun_qwen/custom_openai_compatible；legacy: qwen/doubao
```

Update the switch in `NewClientFromUserConfig`:

```go
	switch strings.ToLower(cfg.Provider) {
	case "qwen", "tongyi", "alibabacloud":
		provider = NewQwenProvider(cfg.APIKey, cfg.Model, timeout)
	case "doubao", "bytedance", "volcengine_native":
		provider = NewDoubaoProvider(cfg.APIKey, cfg.Model, timeout)
	case "openai", "azure", "anthropic", "baidu_qianfan", "volcengine", "aliyun_qwen", "custom_openai_compatible":
		provider = NewOpenAIProvider(cfg.APIKey, cfg.BaseURL, cfg.Model, timeout)
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", cfg.Provider)
	}
```

Do not add `qwen_native`, `doubao_native`, or `qianfan_native` here because v1.4.1 rejects them before client creation.

- [ ] **Step 5: Run factory tests and verify GREEN**

Run:

```bash
go test ./internal/client/llm -count=1
```

Expected: PASS.

Run only if commit authorization has been explicitly granted:

```bash
git add internal/domain/user/llm_config.go internal/client/llm/factory.go internal/client/llm/factory_test.go
git commit -m "feat(llm): route provider presets through openai compatible client"
```

Commit body, when committing, must include:

```text
Co-Authored-By: Claude <noreply@anthropic.com>
```

---

## Task 4: Add provider-options Web API

**Files:**
- Modify: `internal/api/web/handler.go`
- Modify: `internal/api/web/handler_test.go`
- Modify: `internal/api/routes.go`

- [ ] **Step 1: Write failing handler tests**

In `internal/api/web/handler_test.go`, extend `mockLLMConfigService` with provider options support.

Add field:

```go
	providerOptions []serviceuser.ProviderOption
```

Add method:

```go
func (m *mockLLMConfigService) ListProviderOptions() []serviceuser.ProviderOption {
	if m.providerOptions != nil {
		return m.providerOptions
	}
	return []serviceuser.ProviderOption{
		{
			Value:          "openai",
			Label:          "OpenAI 官方",
			Mode:           "openai_compatible",
			Status:         "available",
			DefaultBaseURL: "https://api.openai.com/v1",
			DefaultModel:   "gpt-4o-mini",
		},
		{
			Value:          "qwen_native",
			Label:          "通义千问专用",
			Mode:           "native",
			Status:         "disabled",
			DisabledReason: "专用接口暂不可用，请使用 OpenAI 兼容模式。",
		},
	}
}
```

Append this test:

```go
func TestHandleGetLLMProviders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	llmClient := &mockLLMClient{}
	configSvc := &mockLLMConfigService{
		providerOptions: []serviceuser.ProviderOption{
			{
				Value:          "baidu_qianfan",
				Label:          "百度千帆",
				Mode:           "openai_compatible",
				Status:         "available",
				DefaultBaseURL: "https://qianfan.baidubce.com/v2",
				DefaultModel:   "ernie-4.0-turbo-8k",
			},
			{
				Value:          "qwen_native",
				Label:          "通义千问专用",
				Mode:           "native",
				Status:         "disabled",
				DisabledReason: "专用接口暂不可用，请使用 OpenAI 兼容模式。",
			},
		},
	}

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{})

	router := gin.New()
	router.GET("/api/v1/user/llm-providers", handler.HandleGetLLMProviders)

	req, _ := http.NewRequest("GET", "/api/v1/user/llm-providers", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "baidu_qianfan")
	assert.Contains(t, w.Body.String(), "https://qianfan.baidubce.com/v2")
	assert.Contains(t, w.Body.String(), "qwen_native")
	assert.Contains(t, w.Body.String(), "disabled")
	assert.Contains(t, w.Body.String(), "专用接口暂不可用")
}
```

- [ ] **Step 2: Run handler test and verify RED**

Run:

```bash
go test ./internal/api/web -run TestHandleGetLLMProviders -count=1
```

Expected: FAIL because `HandleGetLLMProviders` is not defined.

- [ ] **Step 3: Extend web handler interface and DTOs**

In `internal/api/web/handler.go`, extend the `LLMConfigService` interface:

```go
	ListProviderOptions() []userLLM.ProviderOption
```

Add response DTOs near the LLM config response structs:

```go
type LLMProviderOptionDTO struct {
	Value          string `json:"value"`
	Label          string `json:"label"`
	Mode           string `json:"mode"`
	Status         string `json:"status"`
	DefaultBaseURL string `json:"default_base_url"`
	DefaultModel   string `json:"default_model"`
	Description    string `json:"description"`
	DisabledReason string `json:"disabled_reason"`
}

type GetLLMProvidersResponse struct {
	Providers []LLMProviderOptionDTO `json:"providers"`
}

func toProviderOptionDTO(option userLLM.ProviderOption) LLMProviderOptionDTO {
	return LLMProviderOptionDTO{
		Value:          option.Value,
		Label:          option.Label,
		Mode:           option.Mode,
		Status:         option.Status,
		DefaultBaseURL: option.DefaultBaseURL,
		DefaultModel:   option.DefaultModel,
		Description:    option.Description,
		DisabledReason: option.DisabledReason,
	}
}
```

Add handler:

```go
func (h *Handler) HandleGetLLMProviders(c *gin.Context) {
	options := h.llmConfigService.ListProviderOptions()
	items := make([]LLMProviderOptionDTO, 0, len(options))
	for _, option := range options {
		items = append(items, toProviderOptionDTO(option))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": GetLLMProvidersResponse{
			Providers: items,
		},
	})
}
```

- [ ] **Step 4: Register route**

In `internal/api/routes.go`, add this route inside `userAPIGroup := r.Group("/api/v1/user")`:

```go
			userAPIGroup.GET("/llm-providers", webHandler.HandleGetLLMProviders)
```

Place it before or after `/llm-config`; keep grouping under `/api/v1/user`.

- [ ] **Step 5: Run handler tests and verify GREEN**

Run:

```bash
go test ./internal/api/web -count=1
```

Expected: PASS.

Run only if commit authorization has been explicitly granted:

```bash
git add internal/api/web/handler.go internal/api/web/handler_test.go internal/api/routes.go
git commit -m "feat(web): expose llm provider preset options"
```

Commit body, when committing, must include:

```text
Co-Authored-By: Claude <noreply@anthropic.com>
```

---

## Task 5: Update frontend types, service, and store

**Files:**
- Create: `frontend/src/types/llmProvider.ts`
- Modify: `frontend/src/types/api.ts`
- Modify: `frontend/src/services/config.ts`
- Modify: `frontend/src/stores/settings.ts`

- [ ] **Step 1: Add frontend provider option types**

Create `frontend/src/types/llmProvider.ts`:

```ts
export type LLMProviderMode = 'openai_compatible' | 'native';
export type LLMProviderStatus = 'available' | 'disabled';

export interface LLMProviderOption {
  value: string;
  label: string;
  mode: LLMProviderMode;
  status: LLMProviderStatus;
  default_base_url: string;
  default_model: string;
  description: string;
  disabled_reason: string;
}

export interface GetLLMProvidersResponse {
  providers: LLMProviderOption[];
}
```

- [ ] **Step 2: Update API types**

In `frontend/src/types/api.ts`, import the provider response type near the top:

```ts
import type { GetLLMProvidersResponse } from './llmProvider';
```

Add this export near the existing user LLM config response types:

```ts
export type UserLLMProvidersResponse = GetLLMProvidersResponse;
```

- [ ] **Step 3: Add config service method**

In `frontend/src/services/config.ts`, update imports:

```ts
import type {
  ApiResponse,
  Config,
  UpdateConfigRequest,
  UserLLMConfigResponse,
  UpdateUserLLMConfigRequest,
  UserLLMProvidersResponse,
} from '../types/api';
```

Add method before `getUserLLMConfig`:

```ts
  async getUserLLMProviders(): Promise<UserLLMProvidersResponse> {
    try {
      const response = await request.get<ApiResponse<UserLLMProvidersResponse>>('/user/llm-providers');
      return response.data.data;
    } catch (error) {
      console.error('Failed to get user LLM providers:', error);
      throw error;
    }
  },
```

- [ ] **Step 4: Extend settings store state and loading**

In `frontend/src/stores/settings.ts`, add import:

```ts
import type { LLMProviderOption } from '../types/llmProvider';
```

Add state after `llmConfig`:

```ts
    const providerOptions = ref<LLMProviderOption[]>([]);
```

Add action before `loadConfig`:

```ts
    const loadProviderOptions = async (): Promise<void> => {
      try {
        const response = await configService.getUserLLMProviders();
        providerOptions.value = response.providers;
      } catch (error) {
        console.error('Failed to load LLM providers:', error);
        providerOptions.value = [
          {
            value: 'openai',
            label: 'OpenAI 官方',
            mode: 'openai_compatible',
            status: 'available',
            default_base_url: 'https://api.openai.com/v1',
            default_model: 'gpt-4o-mini',
            description: 'OpenAI 官方 Chat Completions API。',
            disabled_reason: '',
          },
        ];
        throw error;
      }
    };
```

Update `loadConfig` default OpenAI model from `gpt-3.5-turbo` to `gpt-4o-mini`:

```ts
          provider: 'openai',
          model: 'gpt-4o-mini',
          baseUrl: 'https://api.openai.com/v1',
          temperature: 0.7,
          maxTokens: 2048,
```

Update `clearUserConfig` default similarly:

```ts
        llmConfig.value = {
          provider: 'openai',
          model: 'gpt-4o-mini',
          baseUrl: 'https://api.openai.com/v1',
          temperature: 0.7,
          maxTokens: 2048,
        };
```

Add `providerOptions` and `loadProviderOptions` to the return object:

```ts
      providerOptions,
      loadProviderOptions,
```

- [ ] **Step 5: Run frontend build for type check**

Run:

```bash
npm --prefix frontend run build
```

Expected before SettingsPanel changes: may PASS or fail due unused exports depending on lint/build. If it fails only because SettingsPanel has not consumed providerOptions yet, continue to Task 6 and rerun.

Run only if commit authorization has been explicitly granted and build passes:

```bash
git add frontend/src/types/llmProvider.ts frontend/src/types/api.ts frontend/src/services/config.ts frontend/src/stores/settings.ts
git commit -m "feat(frontend): load llm provider options"
```

Commit body, when committing, must include:

```text
Co-Authored-By: Claude <noreply@anthropic.com>
```

---

## Task 6: Update SettingsPanel UI behavior

**Files:**
- Modify: `frontend/src/components/functional/SettingsPanel.vue`

- [ ] **Step 1: Replace hard-coded provider/model options with computed options**

In `SettingsPanel.vue`, update imports:

```ts
import { computed, ref, watch } from 'vue';
import type { LLMConfig } from '@/types/api';
import type { LLMProviderOption } from '@/types/llmProvider';
```

Replace the current `providerOptions` and `modelOptions` constants with:

```ts
interface ProviderSelectOption {
  label: string;
  value: string;
  disabled?: boolean;
}

interface ProviderSelectGroup {
  type: 'group';
  label: string;
  key: string;
  children: ProviderSelectOption[];
}

type ProviderSelectItem = ProviderSelectOption | ProviderSelectGroup;

const availableProviders = computed(() =>
  settingsStore.providerOptions.filter((item) => item.status === 'available')
);

const disabledProviders = computed(() =>
  settingsStore.providerOptions.filter((item) => item.status === 'disabled')
);

const providerOptions = computed<ProviderSelectItem[]>(() => [
  {
    type: 'group',
    label: 'OpenAI 兼容（可用）',
    key: 'openai-compatible',
    children: availableProviders.value.map((item) => ({
      label: item.label,
      value: item.value,
    })),
  },
  {
    type: 'group',
    label: '专用接口（暂不可用）',
    key: 'native-disabled',
    children: disabledProviders.value.map((item) => ({
      label: `${item.label}（暂不可用）`,
      value: item.value,
      disabled: true,
    })),
  },
]);

const selectedProvider = computed<LLMProviderOption | undefined>(() =>
  settingsStore.providerOptions.find((item) => item.value === localConfig.value.provider)
);

const isNativeProviderSelected = computed(() => selectedProvider.value?.status === 'disabled');

const providerHelpText = computed(() => {
  if (!selectedProvider.value) return '';
  if (selectedProvider.value.status === 'disabled') {
    return selectedProvider.value.disabled_reason || '专用接口暂不可用，请使用 OpenAI 兼容模式。';
  }
  return selectedProvider.value.description;
});
```

- [ ] **Step 2: Load providers when panel opens**

Add this watcher after the store sync watcher:

```ts
watch(
  () => props.visible,
  async (visible) => {
    if (!visible) return;
    try {
      await settingsStore.loadProviderOptions();
      await settingsStore.loadConfig();
    } catch (err) {
      console.error('Failed to load settings panel data:', err);
    }
  },
  { immediate: true }
);
```

- [ ] **Step 3: Add provider-change autofill handler**

Add method before `handleSave`:

```ts
const handleProviderChange = (providerValue: string): void => {
  const provider = settingsStore.providerOptions.find((item) => item.value === providerValue);
  if (!provider) return;

  localConfig.value.provider = provider.value;

  if (provider.status === 'disabled') {
    error(provider.disabled_reason || '专用接口暂不可用，请使用 OpenAI 兼容模式。');
    return;
  }

  localConfig.value.baseUrl = provider.default_base_url;
  localConfig.value.model = provider.default_model;
};
```

Update `handleSave` to block disabled provider selections:

```ts
const handleSave = async () => {
  if (isNativeProviderSelected.value) {
    error(selectedProvider.value?.disabled_reason || '专用接口暂不可用，请使用 OpenAI 兼容模式。');
    return;
  }

  isSaving.value = true;
  try {
    await settingsStore.updateLLMConfig(localConfig.value);
    emit('update-config', { llm: localConfig.value });
    success('配置保存成功');
    emit('close');
  } catch (err) {
    console.error('Failed to save settings:', err);
    error('配置保存失败，请重试');
  } finally {
    isSaving.value = false;
  }
};
```

- [ ] **Step 4: Update template provider/model fields**

Replace provider select block with:

```vue
      <NFormItem label="调用模式 / 服务商">
        <div class="w-full space-y-2">
          <NSelect
            v-model:value="localConfig.provider"
            :options="providerOptions"
            @update:value="handleProviderChange"
          />
          <p v-if="providerHelpText" class="text-xs text-gray-500">
            {{ providerHelpText }}
          </p>
        </div>
      </NFormItem>
```

Replace model select block with an input so users can override recommended models:

```vue
      <NFormItem label="模型">
        <NInput
          v-model:value="localConfig.model"
          placeholder="请输入模型名称，如 qwen-plus"
        />
      </NFormItem>
```

Update save button to disable native provider selections:

```vue
        <NButton
          type="primary"
          @click="handleSave"
          :loading="isSaving"
          :disabled="isNativeProviderSelected"
        >
          保存
        </NButton>
```

Keep Tailwind utility classes for spacing. Do not add inline styles beyond the existing modal width pattern in the file.

- [ ] **Step 5: Run frontend build**

Run:

```bash
npm --prefix frontend run build
```

Expected: PASS.

Run only if commit authorization has been explicitly granted:

```bash
git add frontend/src/components/functional/SettingsPanel.vue
git commit -m "feat(frontend): add openai-compatible provider presets"
```

Commit body, when committing, must include:

```text
Co-Authored-By: Claude <noreply@anthropic.com>
```

---

## Task 7: Update backend API tests for update semantics

**Files:**
- Modify: `internal/api/web/handler_test.go`

- [ ] **Step 1: Add handler update tests for new preset and disabled provider**

Add these subtests inside `TestHandleUpdateLLMConfig`:

```go
	t.Run("update openai compatible preset success", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{hasConfig: false, updateErr: nil}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{})

		router := gin.New()
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
			"session_id":  "test-session-123",
			"provider":    "aliyun_qwen",
			"model":       "qwen-plus",
			"api_key":     "sk-test-key-1234567890abcdefghijk",
			"base_url":    "https://dashscope.aliyuncs.com/compatible-mode/v1",
			"temperature": 0.7,
			"max_tokens":  2048,
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("PUT", "/api/v1/user/llm-config", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "aliyun_qwen", configSvc.lastUpdate.Provider)
		assert.Equal(t, "https://dashscope.aliyuncs.com/compatible-mode/v1", configSvc.lastUpdate.BaseURL)
		assert.Equal(t, "qwen-plus", configSvc.lastUpdate.Model)
	})

	t.Run("native provider disabled error", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{
			hasConfig: false,
			updateErr: errors.New("专用接口暂不可用，请使用 OpenAI 兼容模式。"),
		}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{})

		router := gin.New()
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
			"session_id":  "test-session-123",
			"provider":    "qwen_native",
			"model":       "qwen-plus",
			"api_key":     "sk-test-key-1234567890abcdefghijk",
			"base_url":    "https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation",
			"temperature": 0.7,
			"max_tokens":  2048,
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("PUT", "/api/v1/user/llm-config", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "专用接口暂不可用")
	})
```

Add `errors` to imports if it is not already present:

```go
	"errors"
```

- [ ] **Step 2: Run handler tests**

Run:

```bash
go test ./internal/api/web -run TestHandleUpdateLLMConfig -count=1
```

Expected: PASS after Task 4 changes.

- [ ] **Step 3: Run backend focused tests**

Run:

```bash
go test ./internal/service/user ./internal/client/llm ./internal/api/web -count=1
```

Expected: PASS.

Run only if commit authorization has been explicitly granted:

```bash
git add internal/api/web/handler_test.go
git commit -m "test(web): cover llm provider preset updates"
```

Commit body, when committing, must include:

```text
Co-Authored-By: Claude <noreply@anthropic.com>
```

---

## Task 8: Update architecture docs and changelog

**Files:**
- Modify: `docs/30-服务架构/02-模块设计/用户域/llm-config-service.md`
- Modify: `docs/30-服务架构/02-模块设计/基础设施层/llm-client.md`
- Modify: `docs/90-迭代记录/CHANGELOG.md`

- [ ] **Step 1: Update LLM config service architecture doc**

In `docs/30-服务架构/02-模块设计/用户域/llm-config-service.md`, add a new section after “## 5. LLM 调用时的配置优先级”:

```markdown
## 5.1 v1.4.1 OpenAI 兼容服务商预设

Web 端 LLM 配置从 v1.4.1 开始按“调用模式”组织：

| 分组 | 状态 | Provider 值 |
|------|------|-------------|
| OpenAI 兼容 | 可用 | `openai`、`baidu_qianfan`、`volcengine`、`aliyun_qwen`、`custom_openai_compatible` |
| 专用接口 | 暂不可用 | `qianfan_native`、`qwen_native`、`doubao_native` |

服务层通过 `ListProviderOptions()` 向 Web 端返回服务商预设。Web 端不再自行维护服务商列表，避免前后端默认 Base URL 和模型名不一致。

本期新增的 OpenAI 兼容预设统一保存到 `user_llm_configs.provider`，聊天时通过 `NewClientFromUserConfig` 创建 `OpenAIProvider`，按 `{base_url}/chat/completions` 调用。

专用接口 provider 在 v1.4.1 仅展示不可用状态，保存时返回“专用接口暂不可用，请使用 OpenAI 兼容模式。”。
```

- [ ] **Step 2: Update LLM client architecture doc**

In `docs/30-服务架构/02-模块设计/基础设施层/llm-client.md`, replace the user config section code block with:

```markdown
用户可以通过微信命令或 Web API 配置自己的 LLM：

```go
type UserConfig struct {
    Provider string // openai/baidu_qianfan/volcengine/aliyun_qwen/custom_openai_compatible；legacy: qwen/doubao
    APIKey   string
    BaseURL  string
    Model    string
}
```

`NewClientFromUserConfig` 根据用户配置创建单 provider 客户端（无 fallback）。

v1.4.1 规则：

| Provider | 路由 |
|----------|------|
| `openai` | OpenAIProvider |
| `baidu_qianfan` | OpenAIProvider |
| `volcengine` | OpenAIProvider |
| `aliyun_qwen` | OpenAIProvider |
| `custom_openai_compatible` | OpenAIProvider |
| `qwen` | Legacy QwenProvider，保留兼容旧数据 |
| `doubao` | Legacy DoubaoProvider，保留兼容旧数据 |
```

- [ ] **Step 3: Add changelog entry**

At the top of `docs/90-迭代记录/CHANGELOG.md`, before `## [v1.3.0]`, add:

```markdown
## [v1.4.1] - 2026-06-14

### ✨ 新增功能

- **OpenAI 兼容服务商预设配置**
  - Web 设置面板新增 OpenAI 兼容服务商预设：OpenAI 官方、百度千帆、字节火山、阿里千问、自定义 OpenAI-compatible
  - 预设自动带出默认 Base URL 和推荐模型，用户仍可手动覆盖
  - 专用接口保留展示并标注“暂不可用”
  - 用户保存后的新预设统一走 OpenAI-compatible 调用路由

### 🔧 架构改进

- 新增后端 provider preset registry，前端通过 API 获取服务商选项，减少前后端硬编码不一致
- 用户级新 provider preset ID 统一路由到 `OpenAIProvider`
- 保留 legacy `qwen` / `doubao` 用户配置读取和调用兼容

### 🔐 安全

- API Key 继续 AES-256-GCM 加密存储，接口只返回脱敏信息
- 配置错误提示不暴露 API Key、内部堆栈或完整用户消息
```

- [ ] **Step 4: Review docs consistency**

Read the changed sections and confirm they do not claim native providers are enabled in v1.4.1.

Run only if commit authorization has been explicitly granted:

```bash
git add docs/30-服务架构/02-模块设计/用户域/llm-config-service.md docs/30-服务架构/02-模块设计/基础设施层/llm-client.md docs/90-迭代记录/CHANGELOG.md
git commit -m "docs(llm-config): document provider preset routing"
```

Commit body, when committing, must include:

```text
Co-Authored-By: Claude <noreply@anthropic.com>
```

---

## Task 9: Final verification and graph update

**Files:**
- No source files should be edited in this task unless verification exposes a bug.

- [ ] **Step 1: Run focused backend tests**

Run:

```bash
go test ./internal/service/user ./internal/client/llm ./internal/api/web -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full backend tests**

Run:

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 3: Run frontend production build**

Run:

```bash
npm --prefix frontend run build
```

Expected: PASS.

- [ ] **Step 4: Run local app smoke verification**

Start backend:

```bash
go run cmd/server/main.go -config configs/config.yaml
```

Start frontend:

```bash
npm --prefix frontend run dev -- --host 127.0.0.1
```

Verify provider options API:

```bash
curl -sS http://localhost:8080/api/v1/user/llm-providers
```

Expected response includes:

```json
{
  "success": true,
  "data": {
    "providers": [
      {
        "value": "baidu_qianfan",
        "mode": "openai_compatible",
        "status": "available",
        "default_base_url": "https://qianfan.baidubce.com/v2",
        "default_model": "ernie-4.0-turbo-8k"
      }
    ]
  }
}
```

Save an Aliyun Qwen OpenAI-compatible config:

```bash
curl -sS -X PUT http://localhost:8080/api/v1/user/llm-config \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"smoke-provider-presets","provider":"aliyun_qwen","api_key":"sk-test-key-1234567890abcdefghijklmnop","base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","model":"qwen-plus","temperature":0.7,"max_tokens":2048}'
```

Expected response:

```json
{"message":"配置保存成功","success":true}
```

Read it back:

```bash
curl -sS 'http://localhost:8080/api/v1/user/llm-config?session_id=smoke-provider-presets'
```

Expected response contains `aliyun_qwen`, masked API key, `https://dashscope.aliyuncs.com/compatible-mode/v1`, and `qwen-plus`.

Try disabled native provider:

```bash
curl -sS -X PUT http://localhost:8080/api/v1/user/llm-config \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"smoke-provider-presets","provider":"qwen_native","api_key":"sk-test-key-1234567890abcdefghijklmnop","base_url":"https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation","model":"qwen-plus","temperature":0.7,"max_tokens":2048}'
```

Expected response contains:

```json
{"error":"专用接口暂不可用，请使用 OpenAI 兼容模式。","success":false}
```

- [ ] **Step 5: Update graphify graph after code changes**

Run after source code changes are complete:

```bash
graphify update .
```

Expected: command completes. If `graphify` is not installed or project graph output remains intentionally ignored, report the exact error and continue with test/build evidence.

- [ ] **Step 6: Inspect git status**

Run:

```bash
git status --short
```

Expected: only files from this plan are modified or added. Existing unrelated user changes, if present before execution, must not be altered.

Run only if commit authorization has been explicitly granted:

```bash
git add docs/50-测试/test-plans/v1.4.1-openai-compatible-provider-presets.md \
  internal/domain/user/llm_config.go \
  internal/service/user/llm_provider_preset.go \
  internal/service/user/llm_config_service.go \
  internal/service/user/llm_config_service_test.go \
  internal/client/llm/factory.go \
  internal/client/llm/factory_test.go \
  internal/api/web/handler.go \
  internal/api/web/handler_test.go \
  internal/api/routes.go \
  frontend/src/types/llmProvider.ts \
  frontend/src/types/api.ts \
  frontend/src/services/config.ts \
  frontend/src/stores/settings.ts \
  frontend/src/components/functional/SettingsPanel.vue \
  docs/30-服务架构/02-模块设计/用户域/llm-config-service.md \
  docs/30-服务架构/02-模块设计/基础设施层/llm-client.md \
  docs/90-迭代记录/CHANGELOG.md

git commit -m "feat(llm-config): add openai-compatible provider presets"
```

Commit body, when committing, must include:

```text
Co-Authored-By: Claude <noreply@anthropic.com>
```

---

## Self-review notes

### Spec coverage

- Provider grouping and disabled native providers: Task 4 API, Task 6 UI.
- Provider presets and defaults: Task 2 registry, Task 3 domain defaults, Task 6 autofill.
- Save and chat usage through OpenAI-compatible route: Task 3 factory routing, Task 7 API update tests, Task 9 smoke checks.
- API Key safety and edit-without-key behavior: Task 2 service tests and implementation.
- No test connection in v1.4.1: no task adds test connection endpoint or button.
- Docs and changelog updates: Task 8.
- Runtime verification: Task 9.

### Type consistency

- Backend provider option type: `ProviderOption` in service layer.
- Backend JSON DTO: `LLMProviderOptionDTO` with snake_case JSON fields.
- Frontend API response type: `UserLLMProvidersResponse` aliasing `GetLLMProvidersResponse`.
- Frontend provider option fields match backend JSON: `default_base_url`, `default_model`, `disabled_reason`.

### Placeholder scan

This plan avoids placeholder markers and includes exact paths, commands, expected outputs, and concrete code snippets for each implementation step.
