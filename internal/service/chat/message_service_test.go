package chat

import (
	"context"
	"fmt"
	"testing"

	"wechat-intelligent-bot/internal/domain/conversation"
	"wechat-intelligent-bot/internal/repository/chat"
	"wechat-intelligent-bot/internal/db"
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
