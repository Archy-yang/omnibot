package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
)

func TestBuildTaskReceipt_Completed(t *testing.T) {
	registry := NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type: "researcher", Name: "研究员", Description: "d",
		PromptTemplate: "p", Tools: []string{"rss_reader"}, MaxSteps: 10, Timeout: time.Second,
	}))
	artifact := "Go 1.24 要点"
	task := &domainagent.AgentTask{
		ID: 123, UserID: 1, SubAgentType: "researcher", Goal: "研究 Go 1.24",
		Status: domainagent.TaskStatusCompleted, Artifact: &artifact,
	}

	got := BuildTaskReceipt(registry, task)
	assert.Contains(t, got, "任务ID: 123")
	assert.Contains(t, got, "研究员(researcher)")
	assert.Contains(t, got, "研究 Go 1.24")
	assert.Contains(t, got, "Go 1.24 要点")
}

func TestBuildTaskReceipt_Failed(t *testing.T) {
	registry := NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type: "researcher", Name: "研究员", Description: "d",
		PromptTemplate: "p", Tools: []string{}, MaxSteps: 10, Timeout: time.Second,
	}))
	errMsg := "子 Agent 执行超时"
	task := &domainagent.AgentTask{
		ID: 456, UserID: 1, SubAgentType: "researcher", Goal: "g",
		Status: domainagent.TaskStatusFailed, ErrorMsg: &errMsg,
	}

	got := BuildTaskReceipt(registry, task)
	assert.Contains(t, got, "失败: 子 Agent 执行超时")
	assert.NotContains(t, got, "结果:\n\n") // 不应有空结果
}

func TestBuildTaskReceipt_UnregisteredType(t *testing.T) {
	registry := NewSubAgentRegistry()
	artifact := "r"
	task := &domainagent.AgentTask{
		ID: 1, SubAgentType: "unknown", Goal: "g",
		Status: domainagent.TaskStatusCompleted, Artifact: &artifact,
	}
	got := BuildTaskReceipt(registry, task)
	// 未注册子 Agent,名称回落到类型标识
	assert.Contains(t, got, "unknown(unknown)")
}

func TestBuildReportInstruction_MultipleTasks(t *testing.T) {
	registry := NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type: "researcher", Name: "研究员", Description: "d",
		PromptTemplate: "p", Tools: []string{}, MaxSteps: 10, Timeout: time.Second,
	}))
	a1 := "r1"
	a2 := "r2"
	tasks := []*domainagent.AgentTask{
		{ID: 1, SubAgentType: "researcher", Goal: "g1", Status: domainagent.TaskStatusCompleted, Artifact: &a1},
		{ID: 2, SubAgentType: "researcher", Goal: "g2", Status: domainagent.TaskStatusCompleted, Artifact: &a2},
	}
	got := BuildReportInstruction(registry, tasks)
	assert.Contains(t, got, "管家口吻")
	assert.Contains(t, got, "任务ID: 1")
	assert.Contains(t, got, "任务ID: 2")
	assert.Contains(t, got, "r1")
	assert.Contains(t, got, "r2")
}
