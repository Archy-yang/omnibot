package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"omnibot/internal/db"
	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	"omnibot/internal/repository/chat"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageService_BuildContextMessages(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	service := NewMessageService(msgRepo)

	// 创建 15 轮对话（30 条消息）
	for i := 1; i <= 15; i++ {
		msgRepo.Create(conversation.NewUserMessage(123, fmt.Sprintf("用户消息 %d", i), fmt.Sprintf("wx_%d", i)))
		msgRepo.Create(conversation.NewAssistantMessage(123, fmt.Sprintf("机器人回复 %d", i)))
	}

	// 构建上下文（应该取最近 10 轮 = 20 条消息 + 最新的当前消息）
	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前用户消息")
	if err != nil {
		t.Fatalf("Failed to build context messages: %v", err)
	}

	// 应该有：20 条历史 + 1 条当前 = 21 条
	expectedCount := 20 + 1
	if len(ctxMsgs) != expectedCount {
		t.Errorf("Expected %d context messages, got %d", expectedCount, len(ctxMsgs))
	}

	// 验证第一条历史是第 6 轮的用户消息
	if ctxMsgs[0].Content != "用户消息 6" {
		t.Errorf("Expected first history message to be '用户消息 6', got '%s'", ctxMsgs[0].Content)
	}

	// 验证最后一条是当前消息
	if ctxMsgs[len(ctxMsgs)-1].Content != "当前用户消息" {
		t.Errorf("Expected last message to be current user message, got '%s'", ctxMsgs[len(ctxMsgs)-1].Content)
	}
}

type mockContextMemoryService struct {
	contents []string
	err      error
	limit    int
	userID   int64
}

func (m *mockContextMemoryService) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	return nil, nil
}

func (m *mockContextMemoryService) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	return nil, nil
}

func (m *mockContextMemoryService) Clear(ctx context.Context, userID int64) error {
	return nil
}

func (m *mockContextMemoryService) GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error) {
	m.userID = userID
	m.limit = limit
	return m.contents, m.err
}

func TestMessageService_BuildContextMessages_IncludesLongTermMemories(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	memorySvc := &mockContextMemoryService{contents: []string{"我偏好简洁回答", "我正在开发 OmniBot"}}
	service := NewMessageService(msgRepo, memorySvc)

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前用户消息")

	require.NoError(t, err)
	require.Len(t, ctxMsgs, 2)
	assert.Equal(t, conversation.RoleSystem, ctxMsgs[0].Role)
	assert.Contains(t, ctxMsgs[0].Content, "以下是用户长期记忆")
	assert.Contains(t, ctxMsgs[0].Content, "1. 我偏好简洁回答")
	assert.Contains(t, ctxMsgs[0].Content, "2. 我正在开发 OmniBot")
	assert.Equal(t, conversation.RoleUser, ctxMsgs[1].Role)
	assert.Equal(t, "当前用户消息", ctxMsgs[1].Content)
	assert.Equal(t, int64(123), memorySvc.userID)
	assert.Equal(t, MaxContextMemories, memorySvc.limit)
}

func TestMessageService_BuildContextMessages_UsesRecentTenLongTermMemories(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	memorySvc := &mockContextMemoryService{contents: []string{"记忆 03", "记忆 04", "记忆 05", "记忆 06", "记忆 07", "记忆 08", "记忆 09", "记忆 10", "记忆 11", "记忆 12"}}
	service := NewMessageService(msgRepo, memorySvc)

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前用户消息")

	require.NoError(t, err)
	require.Len(t, ctxMsgs, 2)
	assert.Equal(t, conversation.RoleSystem, ctxMsgs[0].Role)
	assert.Contains(t, ctxMsgs[0].Content, "1. 记忆 03")
	assert.Contains(t, ctxMsgs[0].Content, "10. 记忆 12")
	assert.Equal(t, 10, strings.Count(ctxMsgs[0].Content, "记忆 "))
	assert.Equal(t, MaxContextMemories, memorySvc.limit)
}

func TestMessageService_BuildContextMessages_SkipsMemoryMessageWhenNoLongTermMemories(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	memorySvc := &mockContextMemoryService{contents: []string{}}
	service := NewMessageService(msgRepo, memorySvc)

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前用户消息")

	require.NoError(t, err)
	require.Len(t, ctxMsgs, 1)
	assert.Equal(t, conversation.RoleUser, ctxMsgs[0].Role)
	assert.Equal(t, "当前用户消息", ctxMsgs[0].Content)
}

func TestMessageService_BuildContextMessages_DegradesWhenLongTermMemoryQueryFails(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	memorySvc := &mockContextMemoryService{err: errors.New("memory database down")}
	service := NewMessageService(msgRepo, memorySvc)

	require.NoError(t, msgRepo.Create(conversation.NewUserMessage(123, "历史用户消息", "wx_history")))
	require.NoError(t, msgRepo.Create(conversation.NewAssistantMessage(123, "历史助手回复")))

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前用户消息")

	require.NoError(t, err)
	require.Len(t, ctxMsgs, 3)
	assert.Equal(t, conversation.RoleUser, ctxMsgs[0].Role)
	assert.Equal(t, "历史用户消息", ctxMsgs[0].Content)
	assert.Equal(t, conversation.RoleAssistant, ctxMsgs[1].Role)
	assert.Equal(t, "历史助手回复", ctxMsgs[1].Content)
	assert.Equal(t, conversation.RoleUser, ctxMsgs[2].Role)
	assert.Equal(t, "当前用户消息", ctxMsgs[2].Content)
}

func TestMessageService_SaveUserMessage(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	service := NewMessageService(msgRepo)

	// 第一次保存应该成功
	err := service.SaveUserMessage(context.Background(), 123, "你好", "wx_123")
	if err != nil {
		t.Fatalf("Failed to save user message: %v", err)
	}

	// 重复 MsgID 应该返回去重错误
	err = service.SaveUserMessage(context.Background(), 123, "你好", "wx_123")
	if err != ErrDuplicateMessage {
		t.Errorf("Expected ErrDuplicateMessage, got %v", err)
	}
}

func TestMessageService_SaveAssistantMessage(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	service := NewMessageService(msgRepo)

	err := service.SaveAssistantMessage(context.Background(), 123, "你好！有什么可以帮你的？")
	if err != nil {
		t.Fatalf("Failed to save assistant message: %v", err)
	}
}
