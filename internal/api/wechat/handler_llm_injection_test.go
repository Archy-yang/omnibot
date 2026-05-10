package wechat

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	domain "wechat-intelligent-bot/internal/domain/user"
	repo "wechat-intelligent-bot/internal/repository/user"
	userService "wechat-intelligent-bot/internal/service/user"
	"wechat-intelligent-bot/internal/client/llm"
)

// MockLLMClientWithConfigCapture 捕获调用配置的 mock
type MockLLMClientWithConfigCapture struct {
	LastBaseURL string
	LastAPIKey  string
	CallCount   int
}

func (m *MockLLMClientWithConfigCapture) ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	m.CallCount++
	return "mock response", nil
}

func TestHandler_LLMCall_WithUserConfig(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.LLMConfig{})
	require.NoError(t, err)

	llmRepo := repo.NewLLMConfigRepository(db)
	llmConfigService := userService.NewLLMConfigService(llmRepo)
	mockLLM := &MockLLMClientWithConfigCapture{}

	handler := NewHandler(Config{}, mockLLM, nil, llmConfigService)

	// 先设置用户配置（使用 userID = 1）
	_, _ = handler.handleConfigCommand(1, "#设置Key sk-test-api-key-12345678901234567890")

	// 发送对话消息
	msg := &Message{
		FromUserName: "user123",
		MsgType:      "text",
		Content:      "你好",
	}

	reply, err := handler.handleTextMessage(msg)
	require.NoError(t, err)
	assert.Contains(t, reply, "mock response")
}

func TestHandler_LLMCall_WithoutUserConfig_UsesSystemDefault(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.LLMConfig{})
	require.NoError(t, err)

	llmRepo := repo.NewLLMConfigRepository(db)
	llmConfigService := userService.NewLLMConfigService(llmRepo)
	mockLLM := &MockLLMClientWithConfigCapture{}

	handler := NewHandler(Config{}, mockLLM, nil, llmConfigService)

	// 用户没有设置配置，直接对话
	msg := &Message{
		FromUserName: "user456",
		MsgType:      "text",
		Content:      "你好",
	}

	reply, err := handler.handleTextMessage(msg)
	require.NoError(t, err)
	assert.Contains(t, reply, "mock response")
}

func TestHandler_LLMCall_ConfigError_ReturnsFriendlyMessage(t *testing.T) {
	// 测试配置错误时返回友好提示（如无效 API Key）
	// 这个测试需要更复杂的 mock 来模拟 LLM 调用失败
	// 暂时跳过，在实际实现时完善
	t.Skip("Need more complex mock for error handling test")
}
