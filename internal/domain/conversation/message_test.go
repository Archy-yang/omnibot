package conversation

import (
	"testing"
)

func TestMessage_NewUserMessage(t *testing.T) {
	userID := int64(123)
	content := "你好"
	msgID := "wx_msg_123"

	msg := NewUserMessage(userID, content, msgID)

	if msg.UserID != userID {
		t.Errorf("Expected UserID %d, got %d", userID, msg.UserID)
	}
	if msg.Role != RoleUser {
		t.Errorf("Expected Role %s, got %s", RoleUser, msg.Role)
	}
	if msg.Content != content {
		t.Errorf("Expected Content %s, got %s", content, msg.Content)
	}
	if msg.MsgID == nil || *msg.MsgID != msgID {
		t.Errorf("Expected MsgID %s, got %v", msgID, msg.MsgID)
	}
	if msg.CreatedAt.IsZero() {
		t.Error("Expected CreatedAt to be set")
	}
}

func TestMessage_NewAssistantMessage(t *testing.T) {
	userID := int64(123)
	content := "你好！有什么可以帮你的？"

	msg := NewAssistantMessage(userID, content)

	if msg.UserID != userID {
		t.Errorf("Expected UserID %d, got %d", userID, msg.UserID)
	}
	if msg.Role != RoleAssistant {
		t.Errorf("Expected Role %s, got %s", RoleAssistant, msg.Role)
	}
	if msg.Content != content {
		t.Errorf("Expected Content %s, got %s", content, msg.Content)
	}
	if msg.MsgID != nil {
		t.Errorf("Expected MsgID to be nil for assistant, got %v", msg.MsgID)
	}
}

// TestMessage_NewUserMessage_EmptyMsgID 确保无 msgID 的渠道（如 Web）
// 不会因为空字符串与 msg_id 唯一索引冲突，导致从第二条消息开始入库失败。
func TestMessage_NewUserMessage_EmptyMsgID(t *testing.T) {
	msg := NewUserMessage(123, "hello", "")

	if msg.MsgID != nil {
		t.Errorf("Expected MsgID to be nil when msgID is empty, got %v (*MsgID=%q)", msg.MsgID, *msg.MsgID)
	}
}
