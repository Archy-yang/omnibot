package chat

import (
	"fmt"
	"testing"

	"wechat-intelligent-bot/internal/domain/conversation"
	"wechat-intelligent-bot/internal/db"
)

func TestMessageRepository_Create(t *testing.T) {
	testDB := db.NewTestDB(t)
	repo := NewMessageRepository(testDB)

	msg := conversation.NewUserMessage(123, "你好", "wx_123")

	err := repo.Create(msg)
	if err != nil {
		t.Fatalf("Failed to create message: %v", err)
	}
	if msg.ID == 0 {
		t.Error("Expected message ID to be set after create")
	}
}

func TestMessageRepository_GetRecentByUserID(t *testing.T) {
	testDB := db.NewTestDB(t)
	repo := NewMessageRepository(testDB)

	// 创建 15 条消息（15 轮）
	for i := 1; i <= 15; i++ {
		userMsg := conversation.NewUserMessage(123, fmt.Sprintf("用户消息 %d", i), fmt.Sprintf("wx_%d", i))
		repo.Create(userMsg)
		assistantMsg := conversation.NewAssistantMessage(123, fmt.Sprintf("机器人回复 %d", i))
		repo.Create(assistantMsg)
	}

	// 查询最近 10 条（应该返回消息 11-15，共 10 条）
	messages, err := repo.GetRecentByUserID(123, 10)
	if err != nil {
		t.Fatalf("Failed to get recent messages: %v", err)
	}
	if len(messages) != 10 {
		t.Errorf("Expected 10 messages, got %d", len(messages))
	}
	// 验证是按时间正序排列（最旧的在前，最新的在后）
	if messages[0].Content != "用户消息 11" {
		t.Errorf("Expected first message to be '用户消息 11', got '%s'", messages[0].Content)
	}
	if messages[9].Content != "机器人回复 15" {
		t.Errorf("Expected last message to be '机器人回复 15', got '%s'", messages[9].Content)
	}
}

func TestMessageRepository_ExistsByMsgID(t *testing.T) {
	testDB := db.NewTestDB(t)
	repo := NewMessageRepository(testDB)

	// 创建一条消息
	msg := conversation.NewUserMessage(123, "你好", "wx_123")
	repo.Create(msg)

	// 验证存在
	exists, err := repo.ExistsByMsgID("wx_123")
	if err != nil {
		t.Fatalf("Failed to check exists: %v", err)
	}
	if !exists {
		t.Error("Expected message to exist")
	}

	// 验证不存在
	exists, err = repo.ExistsByMsgID("wx_not_exists")
	if err != nil {
		t.Fatalf("Failed to check not exists: %v", err)
	}
	if exists {
		t.Error("Expected message to not exist")
	}
}
