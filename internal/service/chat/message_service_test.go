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

// TestMessageService_SaveUserMessage_EmptyMsgID 回归测试：Web 渠道无 msgID 时
// 多条消息都应正常入库，不应被空字符串去重命中或被唯一索引拦截。
func TestMessageService_SaveUserMessage_EmptyMsgID(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	service := NewMessageService(msgRepo)

	require.NoError(t, service.SaveUserMessage(context.Background(), 123, "第一条", ""))
	require.NoError(t, service.SaveUserMessage(context.Background(), 123, "第二条", ""))
	require.NoError(t, service.SaveUserMessage(context.Background(), 123, "第三条", ""))

	messages, err := msgRepo.GetRecentByUserID(123, 10)
	require.NoError(t, err)
	require.Len(t, messages, 3)
	assert.Equal(t, "第一条", messages[0].Content)
	assert.Equal(t, "第二条", messages[1].Content)
	assert.Equal(t, "第三条", messages[2].Content)
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

// TestMessageService_SaveAssistantMessageWithSegments 验证带 segments 的助手消息
// 落库后能原样读回（往返一致），含 tool 段的工具名/文案/结果（v1.5.4）。
func TestMessageService_SaveAssistantMessageWithSegments(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	service := NewMessageService(msgRepo)

	segments := []conversation.MessageSegment{
		{Type: "text", Content: "让我查一下。"},
		{Type: "tool", Tool: "get_current_time", Label: "查询了当前时间", Result: "10:30"},
		{Type: "text", Content: "现在是 10:30。"},
	}

	err := service.SaveAssistantMessageWithSegments(
		context.Background(), 123, "让我查一下。现在是 10:30。", segments, nil,
	)
	if err != nil {
		t.Fatalf("Failed to save assistant message with segments: %v", err)
	}

	got, err := msgRepo.GetRecentByUserID(123, 10)
	if err != nil {
		t.Fatalf("Failed to read back: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(got))
	}
	if len(got[0].Segments) != 3 {
		t.Fatalf("Expected 3 segments round-trip, got %d", len(got[0].Segments))
	}
	if got[0].Segments[1].Type != "tool" || got[0].Segments[1].Tool != "get_current_time" ||
		got[0].Segments[1].Result != "10:30" {
		t.Errorf("tool segment round-trip mismatch: %+v", got[0].Segments[1])
	}
}

// TestMessageService_SaveAssistantMessageWithSegments_AgentSteps 验证：保存消息时
// 同时落 Agent 运行步骤链，按 seq 有序，且 MessageID 正确关联（v1.5.5）。
func TestMessageService_SaveAssistantMessageWithSegments_AgentSteps(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	stepRepo := chat.NewAgentStepRepository(testDB)
	service := NewMessageService(msgRepo, stepRepo)

	segments := []conversation.MessageSegment{
		{Type: "tool", Tool: "rss_reader", Label: "读取了 RSS 订阅", Result: "工具执行失败"},
	}
	steps := []*conversation.AgentStep{
		func() *conversation.AgentStep { s := conversation.NewLLMStep(0, `[{"role":"user"}]`, `{"tool_calls":[]}`, "gpt-4o", conversation.StepStatusSuccess, 300); s.Seq = 0; return s }(),
		func() *conversation.AgentStep {
			s := conversation.NewToolStep(0, "rss_reader", `{"url":"x"}`, "工具执行错误: dial tcp refused", conversation.StepStatusError, 1200)
			s.Seq = 1
			return s
		}(),
	}

	err := service.SaveAssistantMessageWithSegments(
		context.Background(), 123, "抱歉，读取失败了。", segments, steps,
	)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}

	msgs, err := msgRepo.GetRecentByUserID(123, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("GetRecentByUserID: %v, len=%d", err, len(msgs))
	}
	savedMsgID := msgs[0].ID

	chain, err := stepRepo.ListByMessageID(savedMsgID)
	if err != nil {
		t.Fatalf("ListByMessageID: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(chain))
	}
	// MessageID 关联 + 按 seq 有序：llm_call → tool_call
	if chain[0].MessageID == nil || *chain[0].MessageID != savedMsgID || chain[0].Kind != conversation.StepKindLLMCall {
		t.Errorf("step[0] = %+v, want llm_call linked to msg %d", chain[0], savedMsgID)
	}
	if chain[1].Kind != conversation.StepKindToolCall {
		t.Errorf("step[1] kind = %q, want tool_call", chain[1].Kind)
	}
	// 工具步骤 response 保留完整原始（含真实错误，未脱敏）
	if chain[1].Response != "工具执行错误: dial tcp refused" {
		t.Errorf("tool Response = %q, want 原始未脱敏", chain[1].Response)
	}
	if chain[1].Status != conversation.StepStatusError {
		t.Errorf("Status = %q", chain[1].Status)
	}
}

// TestMessageService_BuildContextMessages_RebuildsToolCallsPair 规范改造:
// assistant 消息带 ToolCalls 时,BuildContextMessages 应展开成 OpenAI 配对序列
// assistant(tool_calls) -> tool(result) -> assistant(最终回复),让下一轮 LLM 能看到调过工具。
func TestMessageService_BuildContextMessages_RebuildsToolCallsPair(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	service := NewMessageService(msgRepo)

	// 造一条带 tool_calls 的 assistant 消息(模拟主 Agent 调了 delegate)
	toolCallsJSON := `[{"id":"call_1","name":"delegate","arguments":"{\"sub_agent_type\":\"researcher\",\"goal\":\"研究X\"}","result":"{\"task_id\":7,\"status\":\"pending\"}"}]`
	msg := conversation.NewAssistantMessageWithSegments(123, "已安排研究员处理X,稍后汇报", nil)
	msg.ToolCalls = &toolCallsJSON
	require.NoError(t, msgRepo.Create(msg))

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "下一个问题")
	require.NoError(t, err)

	// 期望序列:assistant(tool_calls) + tool(result) + assistant(最终回复) + user(当前)
	// 找到带 tool_calls 的 assistant
	var foundToolCalls, foundTool bool
	for _, m := range ctxMsgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			foundToolCalls = true
			require.Len(t, m.ToolCalls, 1)
			fn := m.ToolCalls[0]["function"].(map[string]interface{})
			assert.Equal(t, "delegate", fn["name"])
		}
		if m.Role == "tool" {
			foundTool = true
			assert.Equal(t, "call_1", m.ToolCallID)
			assert.Contains(t, m.Content, "task_id")
		}
	}
	assert.True(t, foundToolCalls, "应展开出带 tool_calls 的 assistant 消息")
	assert.True(t, foundTool, "应展开出 tool 消息(工具结果)")
	// 最终回复仍在
	assert.Contains(t, ctxMsgs[len(ctxMsgs)-1].Content, "下一个问题")
}

// TestMessageService_BuildContextMessages_NoToolCallsForPlainAssistant 规范改造:
// 普通 assistant 消息(无 ToolCalls)不展开,只取 content。
func TestMessageService_BuildContextMessages_NoToolCallsForPlainAssistant(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	service := NewMessageService(msgRepo)

	msgRepo.Create(conversation.NewUserMessage(123, "你好", ""))
	msgRepo.Create(conversation.NewAssistantMessage(123, "你好,有什么可以帮你"))

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "下一个问题")
	require.NoError(t, err)
	// 不应有 tool role 消息
	for _, m := range ctxMsgs {
		assert.NotEqual(t, "tool", m.Role, "普通消息不应展开 tool")
		assert.Empty(t, m.ToolCalls, "普通 assistant 不应有 tool_calls")
	}
}
