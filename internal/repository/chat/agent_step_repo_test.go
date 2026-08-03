package chat

import (
	"testing"

	"omnibot/internal/db"
	"omnibot/internal/domain/conversation"
)

func int64p(v int64) *int64 { return &v }


// TestAgentStepRepository_CreateBatchAndListByMessage 验证一轮对话的步骤链按 seq 有序写入读回。
func TestAgentStepRepository_CreateBatchAndListByMessage(t *testing.T) {
	testDB := db.NewTestDB(t)
	repo := NewAgentStepRepository(testDB)

	steps := []*conversation.AgentStep{
		{UserID: 42, MessageID: int64p(100), Seq: 0, Kind: conversation.StepKindLLMCall,
			Status: conversation.StepStatusSuccess, Request: `[{"role":"user"}]`,
			Response: `{"tool_calls":[...]}`, Model: "gpt-4o", DurationMs: 300},
		{UserID: 42, MessageID: int64p(100), Seq: 1, Kind: conversation.StepKindToolCall,
			Status: conversation.StepStatusError, Tool: "rss_reader",
			Request: `{"url":"x"}`, Response: "dial tcp refused", DurationMs: 1200},
		{UserID: 42, MessageID: int64p(100), Seq: 2, Kind: conversation.StepKindLLMCall,
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
		{UserID: 7, MessageID: int64p(1), Seq: 0, Kind: conversation.StepKindLLMCall, Status: conversation.StepStatusSuccess},
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

// TestAgentStepRepository_ListByTaskID 验证子 Agent 步骤按 task_id 关联 + MessageID 为 nil。
func TestAgentStepRepository_ListByTaskID(t *testing.T) {
	testDB := db.NewTestDB(t)
	repo := NewAgentStepRepository(testDB)

	taskID := int64(500)
	steps := []*conversation.AgentStep{
		{UserID: 42, TaskID: &taskID, Seq: 0, Kind: conversation.StepKindLLMCall,
			Status: conversation.StepStatusSuccess, Request: `[]`, Response: `{}`, Model: "deepseek", DurationMs: 100},
		{UserID: 42, TaskID: &taskID, Seq: 1, Kind: conversation.StepKindToolCall,
			Status: conversation.StepStatusSuccess, Tool: "web_read",
			Request: `{"url":"x"}`, Response: "正文", DurationMs: 500},
	}
	if err := repo.CreateBatch(steps); err != nil {
		t.Fatalf("CreateBatch: %v", err)
	}

	chain, err := repo.ListByTaskID(taskID)
	if err != nil {
		t.Fatalf("ListByTaskID: %v", err)
	}
	if len(chain) != 2 {
		t.Fatalf("chain len = %d, want 2", len(chain))
	}
	// 子 Agent 步骤 MessageID 应为 nil
	if chain[0].MessageID != nil {
		t.Errorf("sub agent step MessageID = %v, want nil", *chain[0].MessageID)
	}
	if chain[0].TaskID == nil || *chain[0].TaskID != taskID {
		t.Errorf("TaskID = %v, want %d", chain[0].TaskID, taskID)
	}
	if chain[1].Tool != "web_read" {
		t.Errorf("step[1] Tool = %q, want web_read", chain[1].Tool)
	}
}

// TestAgentStepRepository_MainAndSubAgentStepsIsolated 主/子 Agent 步骤互不干扰:
// ListByMessageID 只返主 Agent 步骤,ListByTaskID 只返子 Agent 步骤。
func TestAgentStepRepository_MainAndSubAgentStepsIsolated(t *testing.T) {
	testDB := db.NewTestDB(t)
	repo := NewAgentStepRepository(testDB)

	msgID := int64(100)
	taskID := int64(200)
	// 主 Agent 步骤(MessageID=msgID, TaskID=nil)
	repo.CreateBatch([]*conversation.AgentStep{
		{UserID: 1, MessageID: &msgID, Seq: 0, Kind: conversation.StepKindLLMCall, Status: "success"},
	})
	// 子 Agent 步骤(TaskID=taskID, MessageID=nil)
	repo.CreateBatch([]*conversation.AgentStep{
		{UserID: 1, TaskID: &taskID, Seq: 0, Kind: conversation.StepKindToolCall, Tool: "rss_reader", Status: "success"},
	})

	mainChain, _ := repo.ListByMessageID(msgID)
	if len(mainChain) != 1 || mainChain[0].Tool != "" {
		t.Errorf("ListByMessageID 应只返主 Agent 步骤, got %d steps", len(mainChain))
	}
	subChain, _ := repo.ListByTaskID(taskID)
	if len(subChain) != 1 || subChain[0].Tool != "rss_reader" {
		t.Errorf("ListByTaskID 应只返子 Agent 步骤, got %d steps", len(subChain))
	}
}
