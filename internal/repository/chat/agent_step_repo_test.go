package chat

import (
	"testing"

	"omnibot/internal/db"
	"omnibot/internal/domain/conversation"
)

// TestAgentStepRepository_CreateBatchAndListByMessage 验证一轮对话的步骤链按 seq 有序写入读回。
func TestAgentStepRepository_CreateBatchAndListByMessage(t *testing.T) {
	testDB := db.NewTestDB(t)
	repo := NewAgentStepRepository(testDB)

	steps := []*conversation.AgentStep{
		{UserID: 42, MessageID: 100, Seq: 0, Kind: conversation.StepKindLLMCall,
			Status: conversation.StepStatusSuccess, Request: `[{"role":"user"}]`,
			Response: `{"tool_calls":[...]}`, Model: "gpt-4o", DurationMs: 300},
		{UserID: 42, MessageID: 100, Seq: 1, Kind: conversation.StepKindToolCall,
			Status: conversation.StepStatusError, Tool: "rss_reader",
			Request: `{"url":"x"}`, Response: "dial tcp refused", DurationMs: 1200},
		{UserID: 42, MessageID: 100, Seq: 2, Kind: conversation.StepKindLLMCall,
			Status: conversation.StepStatusSuccess, Request: `[...]`,
			Response: `{"content":"抱歉"}`, Model: "gpt-4o", DurationMs: 250},
	}
	if err := repo.CreateBatch(steps); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	chain, err := repo.ListByMessageID(100)
	if err != nil {
		t.Fatalf("ListByMessageID: %v", err)
	}
	if len(chain) != 3 {
		t.Fatalf("chain len = %d, want 3", len(chain))
	}
	// 按 seq 有序：llm_call → tool_call → llm_call
	if chain[0].Kind != conversation.StepKindLLMCall || chain[0].Seq != 0 {
		t.Errorf("step[0] = %+v, want llm_call seq0", chain[0])
	}
	if chain[1].Kind != conversation.StepKindToolCall || chain[1].Seq != 1 {
		t.Errorf("step[1] = %+v, want tool_call seq1", chain[1])
	}
	// 工具步骤 response 原始未脱敏
	if chain[1].Response != "dial tcp refused" {
		t.Errorf("tool response = %q, want 原始", chain[1].Response)
	}
	if chain[2].Kind != conversation.StepKindLLMCall || chain[2].Seq != 2 {
		t.Errorf("step[2] = %+v, want llm_call seq2", chain[2])
	}
}

// TestAgentStepRepository_ListByUserID 按用户读回。
func TestAgentStepRepository_ListByUserID(t *testing.T) {
	testDB := db.NewTestDB(t)
	repo := NewAgentStepRepository(testDB)

	_ = repo.CreateBatch([]*conversation.AgentStep{
		{UserID: 7, MessageID: 1, Seq: 0, Kind: conversation.StepKindLLMCall, Status: conversation.StepStatusSuccess},
	})
	got, err := repo.ListByUserID(7, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListByUserID: %v, len=%d", err, len(got))
	}
}

// TestAgentStepRepository_CreateBatchEmpty 空切片直接返回 nil。
func TestAgentStepRepository_CreateBatchEmpty(t *testing.T) {
	testDB := db.NewTestDB(t)
	repo := NewAgentStepRepository(testDB)
	if err := repo.CreateBatch(nil); err != nil {
		t.Errorf("CreateBatch(nil) = %v, want nil", err)
	}
}
