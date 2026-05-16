package wechat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	domain "omnibot/internal/domain/user"
	repo "omnibot/internal/repository/user"
	userService "omnibot/internal/service/user"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.LLMConfig{})
	require.NoError(t, err)
	return db
}

func TestHandler_ConfigCommands_SetAPIKey(t *testing.T) {
	db := setupTestDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	llmConfigService := userService.NewLLMConfigService(llmRepo)

	// 创建 mock handler
	handler := &Handler{
		llmConfigService: llmConfigService,
	}

	// 测试设置 API Key 命令
	reply, handled := handler.handleConfigCommand(1, "#设置Key sk-test-api-key-12345678901234567890")
	assert.True(t, handled)
	assert.Contains(t, reply, "API Key 设置成功")

	// 验证配置已保存
	key, _, _, hasCustom, _ := llmConfigService.GetConfigForUser(1) // userID 使用 0 作为占位
	assert.True(t, hasCustom)
	assert.Equal(t, "sk-test-api-key-12345678901234567890", key)
}

func TestHandler_ConfigCommands_ConfigMenu(t *testing.T) {
	db := setupTestDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	llmConfigService := userService.NewLLMConfigService(llmRepo)

	handler := &Handler{
		llmConfigService: llmConfigService,
	}

	reply, handled := handler.handleConfigCommand(1, "#模型设置")
	assert.True(t, handled)
	assert.Contains(t, reply, "模型设置")
	assert.Contains(t, reply, "设置 API Key")
	assert.Contains(t, reply, "设置 API 地址")
}

func TestHandler_ConfigCommands_GetConfigView(t *testing.T) {
	db := setupTestDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	llmConfigService := userService.NewLLMConfigService(llmRepo)

	handler := &Handler{
		llmConfigService: llmConfigService,
	}

	// 先设置配置
	_, _ = handler.handleConfigCommand(1, "#设置Key sk-test-api-key-12345678901234567890")

	// 查看配置
	reply, handled := handler.handleConfigCommand(1, "#我的配置")
	assert.True(t, handled)
	assert.Contains(t, reply, "当前配置")
	assert.Contains(t, reply, "...") // 脱敏标识
}

func TestHandler_ConfigCommands_ClearConfig(t *testing.T) {
	db := setupTestDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	llmConfigService := userService.NewLLMConfigService(llmRepo)

	handler := &Handler{
		llmConfigService: llmConfigService,
	}

	// 先设置配置
	_, _ = handler.handleConfigCommand(1, "#设置Key sk-test-api-key-12345678901234567890")

	// 重置配置
	reply, handled := handler.handleConfigCommand(1, "#重置模型")
	assert.True(t, handled)
	assert.Contains(t, reply, "已重置为系统默认模型")

	// 验证配置已清除
	_, _, _, hasCustom, _ := llmConfigService.GetConfigForUser(1)
	assert.False(t, hasCustom)
}
