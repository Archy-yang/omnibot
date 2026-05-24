package wechat

import (
	"context"
	"errors"
	"strings"
	"testing"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/internal/domain/user"
	memorysvc "omnibot/internal/service/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMemoryService struct {
	rememberedUserID  int64
	rememberedContent string
	rememberErr       error
	listMemories      []*memorydomain.Memory
	listErr           error
	clearUserID       int64
	clearErr          error
}

func (m *mockMemoryService) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	m.rememberedUserID = userID
	m.rememberedContent = content
	if m.rememberErr != nil {
		return nil, m.rememberErr
	}
	memory := memorydomain.NewMemory(userID, content)
	memory.ID = 1
	return memory, nil
}

func (m *mockMemoryService) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	return m.listMemories, m.listErr
}

func (m *mockMemoryService) Clear(ctx context.Context, userID int64) error {
	m.clearUserID = userID
	return m.clearErr
}

func (m *mockMemoryService) GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error) {
	return nil, nil
}

func TestHandler_HandleMemoryCommand_UserIDRequired(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(0, "#记住 我偏好简洁回答")

	require.True(t, handled)
	assert.Equal(t, "服务暂时不可用，请稍后再试", reply)
	assert.Empty(t, memoryService.rememberedContent)
}

func TestHandler_HandleMemoryCommand_DoesNotHandleSimilarRememberPrefix(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#记住一下这个")

	assert.False(t, handled)
	assert.Empty(t, reply)
	assert.Empty(t, memoryService.rememberedContent)
}

func TestHandler_HandleMemoryCommand_RememberSuccess(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#记住 我偏好简洁直接的回答")

	require.True(t, handled)
	assert.Equal(t, int64(42), memoryService.rememberedUserID)
	assert.Equal(t, "我偏好简洁直接的回答", memoryService.rememberedContent)
	assert.Contains(t, reply, "已记住：我偏好简洁直接的回答")
	assert.Contains(t, reply, "请不要保存密码、API Key、身份证号等敏感信息")
}

func TestHandler_HandleMemoryCommand_RememberTrimsLeadingSpaceAfterCommand(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, " #记住    xxx ")

	require.True(t, handled)
	assert.Equal(t, "xxx", memoryService.rememberedContent)
	assert.Contains(t, reply, "已记住：xxx")
}

func TestHandler_HandleMemoryCommand_RememberEmptyContent(t *testing.T) {
	memoryService := &mockMemoryService{rememberErr: memorysvc.ErrEmptyContent}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#记住")

	require.True(t, handled)
	assert.Contains(t, reply, "请在 #记住 后面输入要长期记住的内容")
	assert.Contains(t, reply, "#记住 我偏好简洁直接的回答")
}

func TestHandler_HandleMemoryCommand_RememberTooLong(t *testing.T) {
	memoryService := &mockMemoryService{rememberErr: memorysvc.ErrContentTooLong}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#记住 "+strings.Repeat("你", memorysvc.MaxMemoryContentLength+1))

	require.True(t, handled)
	assert.Equal(t, "这条记忆太长了，请控制在 200 字以内。", reply)
}

func TestHandler_HandleMemoryCommand_ListMemories(t *testing.T) {
	memoryService := &mockMemoryService{listMemories: []*memorydomain.Memory{
		memorydomain.NewMemory(42, "我偏好简洁直接的回答"),
		memorydomain.NewMemory(42, "我正在开发 OmniBot 项目"),
	}}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#我的记忆")

	require.True(t, handled)
	assert.Contains(t, reply, "我目前记住了这些信息：")
	assert.Contains(t, reply, "1. 我偏好简洁直接的回答")
	assert.Contains(t, reply, "2. 我正在开发 OmniBot 项目")
}

func TestHandler_HandleMemoryCommand_ListEmpty(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#我的记忆")

	require.True(t, handled)
	assert.Contains(t, reply, "我还没有长期记住任何信息。")
	assert.Contains(t, reply, "#记住 我偏好简洁直接的回答")
}

func TestHandler_HandleMemoryCommand_Clear(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#清空记忆")

	require.True(t, handled)
	assert.Equal(t, int64(42), memoryService.clearUserID)
	assert.Equal(t, "已清空你的全部长期记忆。", reply)
}

func TestHandler_HandleMemoryCommand_ServiceError(t *testing.T) {
	memoryService := &mockMemoryService{listErr: errors.New("database down")}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#我的记忆")

	require.True(t, handled)
	assert.Equal(t, "服务暂时不可用，请稍后再试", reply)
}

func TestHandler_HandleMemoryCommand_WithoutMemoryServiceDoesNotHandle(t *testing.T) {
	handler := &Handler{}

	reply, handled := handler.handleMemoryCommand(42, "#我的记忆")

	assert.False(t, handled)
	assert.Empty(t, reply)
}

func TestNewHandler_AcceptsMemoryService(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := NewHandler(Config{Token: "testtoken"}, &MockLLMClient{}, &MockUserService{}, memoryService)

	assert.Same(t, memoryService, handler.memoryService)
}

func TestHandler_HandleTextMessage_MemoryCommandDoesNotCallLLM(t *testing.T) {
	memoryService := &mockMemoryService{}
	llmClient := &MockLLMClient{returnString: "LLM reply"}
	mockUser := &MockUserService{returnUser: &user.User{ID: 42}, returnIsNew: false}
	handler := NewHandler(Config{Token: "testtoken"}, llmClient, mockUser, memoryService)

	msg := &Message{
		ToUserName:   "gh_test",
		FromUserName: "openid_test",
		MsgType:      "text",
		Content:      "#记住 我偏好简洁回答",
		MsgID:        "wx_memory_1",
	}

	response, err := handler.handleTextMessage(msg)

	require.NoError(t, err)
	assert.False(t, llmClient.called)
	assert.Contains(t, response, "已记住：我偏好简洁回答")
}
