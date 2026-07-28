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

// TestMessage_NewAssistantMessageWithSegments 验证带 segments 的助手消息构造：
// Role/Content/Segments 正确，MsgID 为 nil（v1.5.4 Agent 思考过程持久化）。
func TestMessage_NewAssistantMessageWithSegments(t *testing.T) {
	userID := int64(123)
	content := "让我查一下。现在是 10:30。"
	segments := []MessageSegment{
		{Type: "text", Content: "让我查一下。"},
		{Type: "tool", Tool: "get_current_time", Label: "查询了当前时间", Result: "10:30"},
		{Type: "text", Content: "现在是 10:30。"},
	}

	msg := NewAssistantMessageWithSegments(userID, content, segments)

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
	if len(msg.Segments) != 3 {
		t.Fatalf("Expected 3 segments, got %d", len(msg.Segments))
	}
	if msg.Segments[0].Type != "text" || msg.Segments[0].Content != "让我查一下。" {
		t.Errorf("Segment[0] mismatch: %+v", msg.Segments[0])
	}
	if msg.Segments[1].Type != "tool" || msg.Segments[1].Tool != "get_current_time" ||
		msg.Segments[1].Label != "查询了当前时间" || msg.Segments[1].Result != "10:30" {
		t.Errorf("Segment[1] mismatch: %+v", msg.Segments[1])
	}
	if msg.CreatedAt.IsZero() {
		t.Error("Expected CreatedAt to be set")
	}
}

// TestNewReportMessage_SetsKindAndTaskID 汇报消息必须 Kind=report + 关联 task_id,
// 以便前端按徽标区分渲染、且能追溯到对应后台任务。
func TestNewReportMessage_SetsKindAndTaskID(t *testing.T) {
	msg := NewReportMessage(7, 99, "调研结果:Go 1.24 支持泛型", nil)

	if msg.Role != RoleAssistant {
		t.Errorf("Expected Role %s, got %s", RoleAssistant, msg.Role)
	}
	if msg.Kind != KindReport {
		t.Errorf("Expected Kind %s, got %q", KindReport, msg.Kind)
	}
	if msg.TaskID == nil || *msg.TaskID != 99 {
		t.Errorf("Expected TaskID 99, got %v", msg.TaskID)
	}
	if msg.Content != "调研结果:Go 1.24 支持泛型" {
		t.Errorf("Expected Content, got %q", msg.Content)
	}
}
