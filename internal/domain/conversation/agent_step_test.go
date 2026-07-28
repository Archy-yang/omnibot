package conversation

import (
	"testing"
)

// TestNewLLMStep 验证 LLM 调用步骤构造（v1.5.5 运行链路记录）。
func TestNewLLMStep(t *testing.T) {
	step := NewLLMStep(
		42,                                  // userID
		`[{"role":"user","content":"hi"}]`,  // request：发出的 messages
		`{"content":"hello"}`,               // response：模型回复
		"gpt-4o",                            // model
		StepStatusSuccess,                   // status
		320,                                 // durationMs
	)

	if step.UserID != 42 {
		t.Errorf("UserID = %d, want 42", step.UserID)
	}
	if step.Kind != StepKindLLMCall {
		t.Errorf("Kind = %q, want %q", step.Kind, StepKindLLMCall)
	}
	if step.Request != `[{"role":"user","content":"hi"}]` {
		t.Errorf("Request = %q", step.Request)
	}
	if step.Response != `{"content":"hello"}` {
		t.Errorf("Response = %q", step.Response)
	}
	if step.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", step.Model)
	}
	if step.Status != StepStatusSuccess {
		t.Errorf("Status = %q", step.Status)
	}
	if step.DurationMs != 320 {
		t.Errorf("DurationMs = %d, want 320", step.DurationMs)
	}
	// Tool 字段对 llm_call 为空
	if step.Tool != "" {
		t.Errorf("Tool should be empty for llm_call, got %q", step.Tool)
	}
	// MessageID/Seq 构造时未知，由上层 stamp
	if step.MessageID != nil || step.Seq != 0 {
		t.Errorf("MessageID/Seq should be nil/0 at construction, got %v/%d", step.MessageID, step.Seq)
	}
	if step.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
}

// TestNewToolStep 验证工具调用步骤构造，response 存原始未脱敏结果。
func TestNewToolStep(t *testing.T) {
	step := NewToolStep(
		42,                                    // userID
		"rss_reader",                          // tool
		`{"url":"https://x"}`,                 // request：arguments
		"工具执行错误: dial tcp refused",      // response：原始未脱敏结果
		StepStatusError,                       // status
		1200,                                  // durationMs
	)

	if step.Kind != StepKindToolCall {
		t.Errorf("Kind = %q, want %q", step.Kind, StepKindToolCall)
	}
	if step.Tool != "rss_reader" {
		t.Errorf("Tool = %q", step.Tool)
	}
	if step.Request != `{"url":"https://x"}` {
		t.Errorf("Request = %q", step.Request)
	}
	if step.Response != "工具执行错误: dial tcp refused" {
		t.Errorf("Response = %q, want 原始未脱敏", step.Response)
	}
	if step.Status != StepStatusError {
		t.Errorf("Status = %q", step.Status)
	}
	if step.DurationMs != 1200 {
		t.Errorf("DurationMs = %d", step.DurationMs)
	}
}

func TestAgentStep_TableName(t *testing.T) {
	if (AgentStep{}).TableName() != "agent_steps" {
		t.Errorf("TableName = %q, want agent_steps", (AgentStep{}).TableName())
	}
}
