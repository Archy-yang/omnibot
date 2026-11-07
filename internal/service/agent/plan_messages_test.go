package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"

	domainagent "omnibot/internal/domain/agent"
)

// 34 号:计划异常/汇总文案(渠道只负责发,不各自拼文案)。

func TestBuildPlanPauseText(t *testing.T) {
	plan := &domainagent.AgentTask{ID: 1, Kind: domainagent.TaskKindPlan, Name: "云南7日行"}
	errMsg := "工具调用超时"
	child := &domainagent.AgentTask{ID: 2, StepSeq: 2, Name: "排路线", Status: domainagent.TaskStatusFailed, ErrorMsg: &errMsg}

	out := BuildPlanPauseText(plan, child, 3)
	assert.Contains(t, out, "云南7日行")
	assert.Contains(t, out, "2/3")
	assert.Contains(t, out, "排路线")
	assert.Contains(t, out, "工具调用超时")
	assert.Contains(t, out, "继续")
}

func TestBuildPlanResultText(t *testing.T) {
	a1 := "大理古城两晚"
	a3 := "最终方案:7 天行程 + 预算"
	plan := &domainagent.AgentTask{ID: 1, Kind: domainagent.TaskKindPlan, Name: "云南7日行"}
	children := []*domainagent.AgentTask{
		{StepSeq: 1, Name: "查资料", Status: domainagent.TaskStatusCompleted, Artifact: &a1},
		{StepSeq: 2, Name: "排路线", Status: domainagent.TaskStatusCompleted, Artifact: &a3},
	}

	out := BuildPlanResultText(plan, children)
	assert.Contains(t, out, "云南7日行")
	assert.Contains(t, out, "共 2 步")
	assert.Contains(t, out, "查资料")
	assert.Contains(t, out, "最终产出")
	assert.Contains(t, out, "最终方案")
}
