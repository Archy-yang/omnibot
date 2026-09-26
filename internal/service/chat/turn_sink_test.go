package chat

import (
	"context"
	"strings"
	"testing"

	"omnibot/internal/db"
	"omnibot/internal/domain/conversation"
	memorysvc "omnibot/internal/service/memory"
	"omnibot/internal/repository/chat"

	"github.com/stretchr/testify/require"
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
	return NewMessageService(msgRepo, MessageServiceDeps{
		TurnSinks: []TurnSink{sink},
	})
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

// TestTurnSink_MultipleSinksAllNotified 注入多个 TurnSink(沉淀管线+消息嵌入器,M7 起)
// 必须全部收到通知。回归背景:turnSink 曾是单字段,后注入的 msgEmbedder 覆盖了
// digestPipeline,导致 M7 上线(2026-09-11)后沉淀管线完全静默、记忆停止总结。
func TestTurnSink_MultipleSinksAllNotified(t *testing.T) {
	sinkA := &fakeTurnSink{}
	sinkB := &fakeTurnSink{}
	msgRepo := chat.NewMessageRepository(db.NewTestDB(t))
	svc := NewMessageService(msgRepo, MessageServiceDeps{
		TurnSinks: []TurnSink{sinkA, sinkB},
	})

	if err := svc.SaveAssistantMessage(context.Background(), 42, "回复"); err != nil {
		t.Fatalf("SaveAssistantMessage: %v", err)
	}
	if len(sinkA.called) != 1 || len(sinkB.called) != 1 {
		t.Fatalf("两个 sink 都应收到通知, A=%v B=%v", sinkA.called, sinkB.called)
	}
}

// TestTurnSink_NilSinkNoPanic 未注入 sink(管线禁用) → 无副作用。
func TestTurnSink_NilSinkNoPanic(t *testing.T) {
	msgRepo := chat.NewMessageRepository(db.NewTestDB(t))
	svc := NewMessageService(msgRepo, MessageServiceDeps{})
	if err := svc.SaveAssistantMessage(context.Background(), 42, "回复"); err != nil {
		t.Fatalf("SaveAssistantMessage: %v", err)
	}
}

// TestTurnSink_UserMessageNoNotify 用户消息落库不触发(一轮以助手回复收尾)。
func TestTurnSink_UserMessageNoNotify(t *testing.T) {
	sink := &fakeTurnSink{}
	svc := turnSinkSetup(t, sink)

	if _, err := svc.SaveUserMessage(context.Background(), 42, "你好", ""); err != nil {
		t.Fatalf("SaveUserMessage: %v", err)
	}
	if len(sink.called) != 0 {
		t.Errorf("用户消息不应触发 NotifyTurn, called=%v", sink.called)
	}
}

// ===== 注入分层测试(PRD 修订:手动常驻+自动存在提示,自动记忆不进 prompt) =====

type fakeInjectionMemory struct {
	manual     []string
	pinnedAuto []string
	autoCount  int
}

func (f *fakeInjectionMemory) GetMemoryInjection(_ context.Context, _ int64) (*memorysvc.MemoryInjection, error) {
	return &memorysvc.MemoryInjection{Manual: f.manual, PinnedAuto: f.pinnedAuto, AutoCount: f.autoCount}, nil
}

func injectionSetup(t *testing.T, mem *fakeInjectionMemory) MessageService {
	t.Helper()
	msgRepo := chat.NewMessageRepository(db.NewTestDB(t))
	return NewMessageService(msgRepo, MessageServiceDeps{
		Memory: mem,
	})
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

// ===== M8.3 pinned 常驻注入(§14.2.4) =====

// TestBuildContextMessages_PinnedAutoResident 置顶自动记忆进常驻注入,
// 提示行条数 = 自动总数 − 已列出置顶数。
func TestBuildContextMessages_PinnedAutoResident(t *testing.T) {
	mem := &fakeInjectionMemory{
		manual:     []string{"我偏好简洁回答"},
		pinnedAuto: []string{"老爷在跟踪十一旅行"},
		autoCount:  3, // 置顶 1 + 未置顶 2
	}
	service := injectionSetup(t, mem)

	msgs, err := service.BuildContextMessages(context.Background(), 123, "hi")
	require.NoError(t, err)

	var memoryBlock string
	for _, m := range msgs {
		if m.Role == conversation.RoleSystem && strings.Contains(m.Content, "长期记忆") {
			memoryBlock = m.Content
		}
	}
	require.NotEmpty(t, memoryBlock, "应有记忆注入块")
	require.Contains(t, memoryBlock, "我偏好简洁回答")
	require.Contains(t, memoryBlock, "老爷在跟踪十一旅行", "置顶自动记忆常驻")
	require.Contains(t, memoryBlock, "另有 2 条", "提示=总数−已列出置顶")
}

// TestBuildContextMessages_PinnedBudgetTruncation 预算超限:manual 全量保留,
// pinned auto 按序截断,被截断条数并入提示行。
func TestBuildContextMessages_PinnedBudgetTruncation(t *testing.T) {
	mem := &fakeInjectionMemory{
		manual:     []string{"手动记忆A"},
		pinnedAuto: []string{"置顶一", "置顶二", "置顶三"},
		autoCount:  10,
	}
	service := injectionSetup(t, mem).(*messageService)
	// 确定性预算:恰好容纳 manual + 第 1 条置顶,后续两条超预算
	pinCost := EstimateTokens("置顶一") + 2
	manualCost := EstimateTokens("手动记忆A") + 2
	service.memoryBlockMaxTokens = manualCost + pinCost + 1

	msgs, err := service.BuildContextMessages(context.Background(), 123, "hi")
	require.NoError(t, err)

	var memoryBlock string
	for _, m := range msgs {
		if m.Role == conversation.RoleSystem && strings.Contains(m.Content, "长期记忆") {
			memoryBlock = m.Content
		}
	}
	require.Contains(t, memoryBlock, "手动记忆A", "manual 全量优先保留")
	require.Contains(t, memoryBlock, "置顶一", "pinned 按 DESC 序保留")
	require.NotContains(t, memoryBlock, "置顶三", "超预算置顶被截断")
	require.Contains(t, memoryBlock, "另有", "截断后保留存在性提示")
}
