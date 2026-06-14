package user

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
	repo "omnibot/internal/repository/user"
)

func setupServiceDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.LLMConfig{})
	require.NoError(t, err)
	return db
}

func TestLLMConfigService_SetAndGetAPIKey(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	err := service.SetAPIKey(1, "sk-test-api-key-1234567890abcdefghijklmnopqrst")
	require.NoError(t, err)

	apiKey, baseURL, model, hasCustom, err := service.GetConfigForUser(1)
	require.NoError(t, err)
	assert.True(t, hasCustom)
	assert.Equal(t, "sk-test-api-key-1234567890abcdefghijklmnopqrst", apiKey)
	assert.Equal(t, "https://api.openai.com/v1", baseURL)
	assert.Equal(t, "gpt-4o-mini", model)
}

func TestLLMConfigService_GetConfigView_Masked(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	_ = service.SetAPIKey(1, "sk-abcdefghijklmnopqrstuvwxyz0123456789")

	view, err := service.GetConfigView(1)
	require.NoError(t, err)
	assert.True(t, view.HasConfig)
	assert.Equal(t, "sk-ab...89", view.APIKeyMasked)
}

func TestLLMConfigService_NoConfig(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	_, _, _, hasCustom, err := service.GetConfigForUser(999)
	require.NoError(t, err)
	assert.False(t, hasCustom)
}

func TestLLMConfigService_ClearConfig(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	_ = service.SetAPIKey(1, "sk-test-key-1234567890abcdefghijklmnopqrst")
	_, _, _, hasCustom, _ := service.GetConfigForUser(1)
	assert.True(t, hasCustom)

	err := service.ClearConfig(1)
	require.NoError(t, err)

	_, _, _, hasCustom, _ = service.GetConfigForUser(1)
	assert.False(t, hasCustom)
}

func TestLLMConfigService_Validation_APIKeyFormat(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	// 不以 sk- 开头的 Key 应该被拒绝
	err := service.SetAPIKey(1, "invalid-key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API Key 必须以 sk- 开头")
}

// ========== UpdateFullConfig 测试 ==========

func TestLLMConfigService_UpdateFullConfig_CreateNew(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	req := UpdateConfigRequest{
		Provider:    "qwen",
		APIKey:      "sk-test-qwen-key-1234567890abcdefghijk",
		BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Model:       "qwen-turbo",
		Temperature: 0.8,
		MaxTokens:   4096,
	}

	err := service.UpdateFullConfig(1, req)
	require.NoError(t, err)

	// 验证配置已保存
	config, hasConfig, err := service.GetFullConfigForUser(1)
	require.NoError(t, err)
	assert.True(t, hasConfig)
	assert.Equal(t, "qwen", config.Provider)
	assert.Equal(t, "sk-test-qwen-key-1234567890abcdefghijk", config.APIKey)
	assert.Equal(t, "https://dashscope.aliyuncs.com/compatible-mode/v1", config.BaseURL)
	assert.Equal(t, "qwen-turbo", config.Model)
	assert.Equal(t, 0.8, config.Temperature)
	assert.Equal(t, 4096, config.MaxTokens)
}

func TestLLMConfigService_UpdateFullConfig_UpdateExisting(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	// 先创建配置
	err := service.SetAPIKey(1, "sk-original-key-1234567890abcdefghijklm")
	require.NoError(t, err)

	// 更新配置
	req := UpdateConfigRequest{
		Provider:    "doubao",
		APIKey:      "sk-new-key-1234567890abcdefghijklmnop",
		BaseURL:     "https://ark.cn-beijing.volcengine.com/api/v3",
		Model:       "doubao-pro-32k",
		Temperature: 0.5,
		MaxTokens:   8192,
	}

	err = service.UpdateFullConfig(1, req)
	require.NoError(t, err)

	// 验证配置已更新
	config, hasConfig, err := service.GetFullConfigForUser(1)
	require.NoError(t, err)
	assert.True(t, hasConfig)
	assert.Equal(t, "doubao", config.Provider)
	assert.Equal(t, "sk-new-key-1234567890abcdefghijklmnop", config.APIKey)
	assert.Equal(t, "doubao-pro-32k", config.Model)
	assert.Equal(t, 0.5, config.Temperature)
	assert.Equal(t, 8192, config.MaxTokens)
}

func TestLLMConfigService_UpdateFullConfig_InvalidProvider(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	req := UpdateConfigRequest{
		Provider:    "invalid-provider",
		APIKey:      "sk-test-key-1234567890abcdefghijklmnop",
		Model:       "some-model",
		Temperature: 0.7,
		MaxTokens:   2048,
	}

	err := service.UpdateFullConfig(1, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "不支持的服务商")
}

func TestLLMConfigService_UpdateFullConfig_InvalidTemperature(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	req := UpdateConfigRequest{
		Provider:    "openai",
		APIKey:      "sk-test-key-1234567890abcdefghijklmnop",
		Model:       "gpt-3.5-turbo",
		Temperature: 3.0, // 超过 0-2 范围
		MaxTokens:   2048,
	}

	err := service.UpdateFullConfig(1, req)
	assert.Error(t, err)
}

func TestLLMConfigService_UpdateFullConfig_InvalidMaxTokens(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	req := UpdateConfigRequest{
		Provider:    "openai",
		APIKey:      "sk-test-key-1234567890abcdefghijklmnop",
		Model:       "gpt-3.5-turbo",
		Temperature: 0.7,
		MaxTokens:   200000, // 超过限制
	}

	err := service.UpdateFullConfig(1, req)
	assert.Error(t, err)
}

func TestLLMConfigService_UpdateFullConfig_InvalidBaseURL(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	req := UpdateConfigRequest{
		Provider:    "openai",
		APIKey:      "sk-test-key-1234567890abcdefghijklmnop",
		BaseURL:     "not-a-url",
		Model:       "gpt-3.5-turbo",
		Temperature: 0.7,
		MaxTokens:   2048,
	}

	err := service.UpdateFullConfig(1, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "必须以 http:// 或 https:// 开头")
}

func TestLLMConfigService_UpdateFullConfig_MissingAPIKeyOnCreate(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	// 首次创建时没有 API Key 应该报错
	req := UpdateConfigRequest{
		Provider:    "openai",
		APIKey:      "", // 空 Key
		Model:       "gpt-3.5-turbo",
		Temperature: 0.7,
		MaxTokens:   2048,
	}

	err := service.UpdateFullConfig(1, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API Key")
}

// ========== GetConfigView 扩展测试 ==========

func TestLLMConfigService_GetConfigView_FullConfig(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	req := UpdateConfigRequest{
		Provider:    "anthropic",
		APIKey:      "sk-anthropic-key-1234567890abcdefghijklmnopqr",
		BaseURL:     "https://api.anthropic.com/v1",
		Model:       "claude-3-sonnet-20240229",
		Temperature: 0.9,
		MaxTokens:   1024,
	}

	err := service.UpdateFullConfig(1, req)
	require.NoError(t, err)

	view, err := service.GetConfigView(1)
	require.NoError(t, err)
	assert.True(t, view.HasConfig)
	assert.Equal(t, "anthropic", view.Provider)
	assert.Equal(t, "https://api.anthropic.com/v1", view.BaseURL)
	assert.Equal(t, "claude-3-sonnet-20240229", view.Model)
	assert.Equal(t, 0.9, view.Temperature)
	assert.Equal(t, 1024, view.MaxTokens)
	assert.Equal(t, "使用你的自定义模型", view.StatusText)
}

// ========== GetFullConfigForUser 测试 ==========

func TestLLMConfigService_GetFullConfigForUser_NoConfig(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	config, hasConfig, err := service.GetFullConfigForUser(999)
	require.NoError(t, err)
	assert.False(t, hasConfig)
	assert.Nil(t, config)
}

// ========== Domain 模型默认值测试 ==========

func TestLLMConfig_DefaultValuesByProvider(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	// 测试通义千问默认值
	err := service.UpdateFullConfig(1, UpdateConfigRequest{
		Provider:    "qwen",
		APIKey:      "sk-test-qwen-key-1234567890abcdefghijklmnopqrs",
		BaseURL:     "", // 空值，应该使用默认
		Model:       "", // 空值，应该使用默认
		Temperature: 0.7,
		MaxTokens:   2048,
	})
	require.NoError(t, err)

	config, hasConfig, err := service.GetFullConfigForUser(1)
	require.NoError(t, err)
	assert.True(t, hasConfig)
	assert.Equal(t, "https://dashscope.aliyuncs.com/compatible-mode/v1", config.BaseURL)
	assert.Equal(t, "qwen-turbo", config.Model)
}

// ========== Provider Preset 测试 ==========

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
		name     string
		provider string
		baseURL  string
		model    string
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
