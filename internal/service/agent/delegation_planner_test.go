package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
)

// mockPlannerClient 模拟规划调用的同步 LLM:直接返回预设的 tool_calls(OpenAI 风格)。
// 规划器只消费 plan_delegation 这个 tool_call,无需文本。
// err 非 nil 时模拟"调用失败"(如默认 provider 空 key),用于验证"传入的活跃 client 覆盖默认"。
type mockPlannerClient struct {
	toolCalls []map[string]interface{}
	err       error
}

func (m *mockPlannerClient) ChatCompletion(ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{}) (string, []map[string]interface{}, error) {
	if m.err != nil {
		return "", nil, m.err
	}
	return "", m.toolCalls, nil
}

// mockTaskStarter 记录 StartTask 调用,返回递增 task_id。校验任务确实被"机械建"了。
type mockTaskStarter struct {
	mu     sync.Mutex
	calls  []domainagent.TaskSpec
	nextID int64
}

func (m *mockTaskStarter) StartTask(ctx context.Context, userID int64, subAgentType string, spec domainagent.TaskSpec, source, notifyTarget string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, spec)
	m.nextID++
	return m.nextID, nil
}

// makePlanToolCall 构造一个 plan_delegation tool_call(OpenAI 风格,arguments 是 tasks 数组)。
// 模拟规划 LLM 返回的调用,供测试注入 mockPlannerClient。
func makePlanToolCall(tasksJSON string) []map[string]interface{} {
	return []map[string]interface{}{
		{
			"id":   "call_plan",
			"type": "function",
			"function": map[string]interface{}{
				"name":      "plan_delegation",
				"arguments": `{"tasks": ` + tasksJSON + `}`,
			},
		},
	}
}

func newTestPlanner(client LLMClient, starter DelegationTaskStarter) *DelegationPlanner {
	reg := NewSubAgentRegistry()
	reg.Register(domainagent.SubAgentCard{
		Type: "researcher", Name: "研究员", Description: "研究任务",
		PromptTemplate: "你是一名研究员。目标:{goal}。", Tools: []string{"web_read"},
	})
	return NewDelegationPlanner(client, starter, reg)
}

// TestDelegationPlanner_PlanAndExecute_UsesProvidedClient P0 bug 回归(agent_steps 1337):
// 规划必须用**传入的活跃 client**(用户自定义/循环同款),而非构造函数里焊死的默认 client。
// 若默认 provider 空 key(系统默认 openai),焊死默认会让规划调用必然失败;传入用户自定义时应能成功建任务。
func TestDelegationPlanner_PlanAndExecute_UsesProvidedClient(t *testing.T) {
	broken := &mockPlannerClient{err: fmt.Errorf("You didn't provide an API key")} // 模拟空 key 的默认 provider
	starter := &mockTaskStarter{}
	reg := NewSubAgentRegistry()
	reg.Register(domainagent.SubAgentCard{Type: "researcher", Name: "研究员", Description: "研究"})
	planner := NewDelegationPlanner(broken, starter, reg)

	// 传入的活跃 client(用户自定义)正常返回计划 -> 应成功建任务(而非用坏的默认 client 退化)。
	good := &mockPlannerClient{toolCalls: makePlanToolCall(
		`[{"sub_agent_type":"researcher","goal":"查高铁"}]`)}
	ids, injected, planStep, err := planner.PlanAndExecute(context.Background(), 1, "帮我查高铁票", good)
	require.NoError(t, err)
	require.Len(t, ids, 1, "传入的活跃 client 应被使用,任务应被创建")
	assert.Equal(t, "查高铁", starter.calls[0].Goal)
	assert.NotEmpty(t, injected)
	require.NotNil(t, planStep)
	assert.Equal(t, StepStatusSuccess, planStep.Status, "用传入 client 规划成功,步骤非 error")
}

// TestDelegationPlanner_PlansAndExecutes 研究类请求:规划返回 1 任务 -> 机械建 1 task,
// 返回 task_id + 注入上下文(告诉主 Agent 已创建了哪些任务)。
func TestDelegationPlanner_PlansAndExecutes(t *testing.T) {
	client := &mockPlannerClient{toolCalls: makePlanToolCall(
		`[{"sub_agent_type":"researcher","goal":"查高铁票"}]`)}
	starter := &mockTaskStarter{}
	planner := newTestPlanner(client, starter)

	ids, injected, planStep, err := planner.PlanAndExecute(context.Background(), 1, "帮我查高铁票")
	require.NoError(t, err)
	require.Len(t, ids, 1)
	assert.Equal(t, int64(1), ids[0], "第一个任务 id 应从 1 开始")
	require.Len(t, starter.calls, 1, "计划里写了任务就必须真正建 task")
	assert.Equal(t, "查高铁票", starter.calls[0].Goal)
	assert.Contains(t, injected, "查高铁票", "注入上下文应含目标,让主 Agent 回复有据可依")
	assert.NotContains(t, injected, "任务ID", "注入上下文不给数字 ID(避免 LLM 回显),改由独立事件下发")
	// 规划这次 LLM 调用必须产出 StepRecord,供写入 agent_steps(复盘可见规划决策)。
	require.NotNil(t, planStep, "规划调用应产出 LLMCall 步骤")
	assert.Equal(t, StepKindLLMCall, planStep.Kind)
	assert.Equal(t, StepStatusSuccess, planStep.Status)
	assert.Contains(t, planStep.Request, "plan_delegation", "步骤 Request 应含规划 system prompt")
	assert.Contains(t, planStep.Response, "plan_delegation", "步骤 Response 应含规划 tool_call")
}

// TestDelegationPlanner_MultipleTasks 计划含 2 任务 -> 建 2 task,顺序保持。
func TestDelegationPlanner_MultipleTasks(t *testing.T) {
	client := &mockPlannerClient{toolCalls: makePlanToolCall(
		`[{"sub_agent_type":"researcher","goal":"查高铁"},{"sub_agent_type":"researcher","goal":"查酒店"}]`)}
	starter := &mockTaskStarter{}
	planner := newTestPlanner(client, starter)

	ids, _, _, err := planner.PlanAndExecute(context.Background(), 1, "订行程")
	require.NoError(t, err)
	require.Len(t, ids, 2)
	assert.Len(t, starter.calls, 2)
	assert.Equal(t, "查高铁", starter.calls[0].Goal)
	assert.Equal(t, "查酒店", starter.calls[1].Goal)
}

// TestDelegationPlanner_PassesDetailedSpec 计划含 deliverables/criteria -> 透传到 TaskSpec。
func TestDelegationPlanner_PassesDetailedSpec(t *testing.T) {
	client := &mockPlannerClient{toolCalls: makePlanToolCall(
		`[{"sub_agent_type":"researcher","goal":"调研三框架",
		   "deliverables":[{"name":"candidate_list","description":"候选框架列表"}],
		   "completion_criteria":["比较三个","给推荐"]}]`)}
	starter := &mockTaskStarter{}
	planner := newTestPlanner(client, starter)

	_, _, _, err := planner.PlanAndExecute(context.Background(), 1, "调研")
	require.NoError(t, err)
	require.Len(t, starter.calls, 1)
	spec := starter.calls[0]
	require.Len(t, spec.Deliverables, 1)
	assert.Equal(t, "candidate_list", spec.Deliverables[0].Name)
	assert.Equal(t, []string{"比较三个", "给推荐"}, spec.CompletionCriteria)
}

// TestDelegationPlanner_EmptyPlan 闲聊:规划返回空 tasks -> 建 0 任务、无注入上下文。
func TestDelegationPlanner_EmptyPlan(t *testing.T) {
	client := &mockPlannerClient{toolCalls: makePlanToolCall(`[]`)}
	starter := &mockTaskStarter{}
	planner := newTestPlanner(client, starter)

	ids, injected, _, err := planner.PlanAndExecute(context.Background(), 1, "你好")
	require.NoError(t, err)
	assert.Len(t, ids, 0)
	assert.Len(t, starter.calls, 0, "空计划绝不能建任务")
	assert.Equal(t, "", injected, "无任务时不注入上下文")
}

// TestDelegationPlanner_NoToolCall 规划 LLM 未返回任何 tool_call -> 优雅降级为 0 任务。
func TestDelegationPlanner_NoToolCall(t *testing.T) {
	client := &mockPlannerClient{toolCalls: nil}
	planner := newTestPlanner(client, &mockTaskStarter{})

	ids, injected, _, err := planner.PlanAndExecute(context.Background(), 1, "hi")
	require.NoError(t, err)
	assert.Len(t, ids, 0)
	assert.Equal(t, "", injected)
}

// TestDelegationPlanner_UnknownTool 规划返回非 plan_delegation 的 tool_call -> 忽略,0 任务。
func TestDelegationPlanner_UnknownTool(t *testing.T) {
	client := &mockPlannerClient{toolCalls: []map[string]interface{}{
		{"id": "x", "type": "function", "function": map[string]interface{}{
			"name": "other_tool", "arguments": `{}`,
		}},
	}}
	planner := newTestPlanner(client, &mockTaskStarter{})

	ids, injected, _, err := planner.PlanAndExecute(context.Background(), 1, "hi")
	require.NoError(t, err)
	assert.Len(t, ids, 0)
	assert.Equal(t, "", injected)
}

// TestDelegationPlanner_InvalidArgs 规划 tool_call 参数是非法 JSON -> 优雅降级,不建任务不报错。
func TestDelegationPlanner_InvalidArgs(t *testing.T) {
	client := &mockPlannerClient{toolCalls: []map[string]interface{}{
		{"id": "x", "type": "function", "function": map[string]interface{}{
			"name": "plan_delegation", "arguments": `not json`,
		}},
	}}
	planner := newTestPlanner(client, &mockTaskStarter{})

	ids, _, _, err := planner.PlanAndExecute(context.Background(), 1, "hi")
	require.NoError(t, err)
	assert.Len(t, ids, 0)
}

// TestAgentService_RunStream_PlannerCreatesAndSeedsTasks 集成胶水(runStreamWithClient):
// 装配规划器的 AgentService.RunStream,进循环前先规划 -> 机械建 task -> 预建 task_id
// 种子进回复末尾。验证"规划器说派就真派,且回复带任务标识"整条链路。
func TestAgentService_RunStream_PlannerCreatesAndSeedsTasks(t *testing.T) {
	plannerClient := &mockPlannerClient{toolCalls: makePlanToolCall(
		`[{"sub_agent_type":"researcher","goal":"查高铁票"}]`)}
	starter := &mockTaskStarter{}
	planner := newTestPlanner(plannerClient, starter)

	// 流式回答 LLM:规划已注入"已创建任务"上下文,主 Agent 直接给出自然语言确认。
	stream := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{{ContentDelta: "好的,已安排研究员去查高铁票"}, {FinishReason: "stop"}, {Done: true}},
		},
	}

	svc := NewAgentService(AgentServiceConfig{
		StreamingLLMClient: stream,
		ToolRegistry:       NewToolRegistry(),
		MaxSteps:           10,
		DelegationPlanner:  planner,
	})

	ch, err := svc.RunStream(context.Background(), 1, []map[string]interface{}{
		{"role": "user", "content": "帮我查高铁票"},
	})
	require.NoError(t, err)

	var final string
	var created []int64
	var planLLMCall *AgentEvent
	for _, e := range drainEvents(t, ch) {
		if e.Type == AgentEventFinal {
			final = e.Content
		}
		if e.Type == AgentEventTaskCreated {
			created = e.TaskIDs
		}
		if e.Type == AgentEventLLMCall && strings.Contains(e.LLMRequest, "plan_delegation") {
			planLLMCall = &e
		}
	}
	require.NotEmpty(t, final)
	require.Len(t, starter.calls, 1, "规划器说派就必须真正建 task(胶水未丢)")
	assert.Equal(t, "查高铁票", starter.calls[0].Goal)
	assert.NotContains(t, final, "任务ID", "任务ID 不应拼进回复文本(独立事件下发)")
	require.Equal(t, []int64{1}, created, "预建 task_id 应作为独立 TaskCreated 事件下发")
	require.NotNil(t, planLLMCall, "规划调用的 LLMCall 必须进事件流(供写入 agent_steps)")
	assert.True(t, planLLMCall.StepDurationMs >= 0)
}

// TestReActAgent_RunStream_SeedsPreCreatedTaskIDs 计划器预先建好任务后,PreCreatedTaskIDs
// 必须种子进 Runtime.DelegateTaskIDs,使回复末尾正确拼接任务标识(与循环内 delegate 创建合并)。
func TestReActAgent_RunStream_SeedsPreCreatedTaskIDs(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// 无 tool_call,直接回复(主 Agent 已从注入上下文知道任务建好了)
			{
				{ContentDelta: "已安排研究员去查高铁票了"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
		PreCreatedTaskIDs:  []int64{43},
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "帮我查高铁票"},
	})
	require.NoError(t, err)

	var finalContent string
	var created []int64
	for _, e := range drainEvents(t, ch) {
		if e.Type == AgentEventFinal {
			finalContent = e.Content
		}
		if e.Type == AgentEventTaskCreated {
			created = e.TaskIDs
		}
	}
	require.NotEmpty(t, finalContent)
	assert.Contains(t, finalContent, "已安排研究员去查高铁票了")
	assert.NotContains(t, finalContent, "任务ID", "PreCreatedTaskIDs 不应拼进回复文本")
	require.Equal(t, []int64{43}, created, "PreCreatedTaskIDs 应种子进 DelegateTaskIDs 并作为独立事件下发")
}
