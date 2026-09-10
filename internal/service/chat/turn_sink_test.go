package chat

import (
	"context"
	"strings"
	"testing"

	"omnibot/internal/db"
	"omnibot/internal/repository/chat"
)

// TurnSink 钩子测试(12-记忆系统技术方案 §7):助手消息落库后通知沉淀管线,异步不阻塞。
type fakeTurnSink struct {
	called []int64
}

func (f *fakeTurnSink) NotifyTurn(userID int64) {
	f.called = append(f.called, userID)
}

func turnSinkSetup(t *testing.T, sink *fakeTurnSink) MessageService {
	t.Helper()
	msgRepo := chat.NewMessageRepository(db.NewTestDB(t))
	return NewMessageService(msgRepo, sink)
}

// TestTurnSink_NotifyOnAssistantSave 助手消息落库成功 → NotifyTurn 被调。
func TestTurnSink_NotifyOnAssistantSave(t *testing.T) {
	sink := &fakeTurnSink{}
	svc := turnSinkSetup(t, sink)

	if err := svc.SaveAssistantMessage(context.Background(), 42, "回复"); err != nil {
		t.Fatalf("SaveAssistantMessage: %v", err)
	}
	if len(sink.called) != 1 || sink.called[0] != 42 {
		t.Fatalf("助手消息落库后应通知管线, called=%v", sink.called)
	}

	if err := svc.SaveAssistantMessageWithSegments(context.Background(), 42, "回复", nil, nil); err != nil {
		t.Fatalf("SaveAssistantMessageWithSegments: %v", err)
	}
	if len(sink.called) != 2 {
		t.Fatalf("WithSegments 也应通知, called=%v", sink.called)
	}

	if err := svc.SaveAssistantMessageWithToolCalls(context.Background(), 42, "回复", nil, nil, nil); err != nil {
		t.Fatalf("SaveAssistantMessageWithToolCalls: %v", err)
	}
	if len(sink.called) != 3 {
		t.Fatalf("WithToolCalls 也应通知, called=%v", sink.called)
	}
}

// TestTurnSink_NilSinkNoPanic 未注入 sink(管线禁用) → 无副作用。
func TestTurnSink_NilSinkNoPanic(t *testing.T) {
	msgRepo := chat.NewMessageRepository(db.NewTestDB(t))
	svc := NewMessageService(msgRepo)
	if err := svc.SaveAssistantMessage(context.Background(), 42, "回复"); err != nil {
		t.Fatalf("SaveAssistantMessage: %v", err)
	}
}

// TestTurnSink_UserMessageNoNotify 用户消息落库不触发(一轮以助手回复收尾)。
func TestTurnSink_UserMessageNoNotify(t *testing.T) {
	sink := &fakeTurnSink{}
	svc := turnSinkSetup(t, sink)

	if err := svc.SaveUserMessage(context.Background(), 42, "你好", ""); err != nil {
		t.Fatalf("SaveUserMessage: %v", err)
	}
	if len(sink.called) != 0 {
		t.Errorf("用户消息不应触发 NotifyTurn, called=%v", sink.called)
	}
}

// ===== 注入分层测试(PRD 修订:手动常驻+自动存在提示,自动记忆不进 prompt) =====

type fakeInjectionMemory struct {
	manual    []string
	autoCount int
}

func (f *fakeInjectionMemory) GetMemoryInjection(_ context.Context, _ int64) ([]string, int, error) {
	return f.manual, f.autoCount, nil
}

func injectionSetup(t *testing.T, mem *fakeInjectionMemory) MessageService {
	t.Helper()
	msgRepo := chat.NewMessageRepository(db.NewTestDB(t))
	return NewMessageService(msgRepo, mem)
}

func findSystemMessage(svc MessageService, userID int64) string {
	msgs, err := svc.BuildContextMessages(context.Background(), userID, "当前消息")
	if err != nil {
		return ""
	}
	for _, m := range msgs {
		if m.Role == "system" {
			return m.Content
		}
	}
	return ""
}

// TestInjection_ManualInjectedAutoHinted 手动记忆全量注入;自动记忆只出存在性提示。
func TestInjection_ManualInjectedAutoHinted(t *testing.T) {
	mem := &fakeInjectionMemory{manual: []string{"用户偏好简洁回复"}, autoCount: 7}
	svc := injectionSetup(t, mem)

	sys := findSystemMessage(svc, 42)
	if !strings.Contains(sys, "用户偏好简洁回复") {
		t.Errorf("手动记忆应注入:\n%s", sys)
	}
	if !strings.Contains(sys, "7") || !strings.Contains(sys, "search_memories") {
		t.Errorf("自动记忆应出存在性提示(含条数与工具名):\n%s", sys)
	}
}

// TestInjection_AutoNotInjected 自动记忆内容不进 prompt(只有条数)。
func TestInjection_AutoNotInjected(t *testing.T) {
	mem := &fakeInjectionMemory{manual: []string{"手动条目"}, autoCount: 3}
	svc := injectionSetup(t, mem)
	sys := findSystemMessage(svc, 42)
	if strings.Contains(sys, "自动记忆的具体内容不应出现") {
		t.Errorf("自动记忆内容不应注入:\n%s", sys)
	}
}

// TestInjection_EmptyMemory 全空 → 不注入 system message。
func TestInjection_EmptyMemory(t *testing.T) {
	mem := &fakeInjectionMemory{}
	svc := injectionSetup(t, mem)
	if sys := findSystemMessage(svc, 42); sys != "" {
		t.Errorf("无记忆不应注入, got:\n%s", sys)
	}
}

// TestInjection_ManualEmptyAutoExists 无手动但有自动 → 仅注入存在性提示。
func TestInjection_ManualEmptyAutoExists(t *testing.T) {
	mem := &fakeInjectionMemory{autoCount: 5}
	svc := injectionSetup(t, mem)
	sys := findSystemMessage(svc, 42)
	if sys == "" || !strings.Contains(sys, "5") || !strings.Contains(sys, "search_memories") {
		t.Errorf("应仅注入存在性提示:\n%s", sys)
	}
}
