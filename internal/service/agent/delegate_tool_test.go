package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
)

// delegate 工具测试:去角色后不传 sub_agent_type/不依赖 registry,只传任务合同 + 可选 persona_hint/task_type。

func setupDelegateToolTest(t *testing.T) (Tool, *SubAgentService) {
	t.Helper()
	db := setupSubAgentServiceTestDB(t)
	repo := repoagent.NewAgentTaskRepository(db)
	// stub runner:立即返回 artifact(不真跑 LLM)。去角色后工具卡 StartTask,不再有角色清单。
	svc := NewSubAgentService(repo, &mockRunner{artifact: "result"}, chatrepo.NewAgentStepRepository(db), nil, nil, nil)
	tool := CreateDelegateTool(svc)
	return tool, svc
}

// TestCreateDelegateTool_DescriptionUniversal 描述是通用执行器,不再列出子 Agent 角色。
func TestCreateDelegateTool_DescriptionUniversal(t *testing.T) {
	tool, _ := setupDelegateToolTest(t)
	assert.Equal(t, "delegate", tool.Name)
	assert.Contains(t, tool.Description, "后台执行器")
	assert.NotContains(t, tool.Description, "researcher")
	assert.NotContains(t, tool.Description, "研究员")
}

// TestCreateDelegateTool_ExecuteStartsTask 不传 sub_agent_type 也能派活(只 goal)。
func TestCreateDelegateTool_ExecuteStartsTask(t *testing.T) {
	tool, _ := setupDelegateToolTest(t)

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{
		"goal": "研究 Go 1.24 新特性",
	})
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))
	assert.NotZero(t, parsed["task_id"])
	assert.Equal(t, "pending", parsed["status"])
}

// TestCreateDelegateTool_PassesTaskSpec delegate 传 deliverables/criteria/background 应落进 task.TaskSpec。
func TestCreateDelegateTool_PassesTaskSpec(t *testing.T) {
	tool, svc := setupDelegateToolTest(t)

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{
		"goal": "研究 Go 框架",
		"deliverables": []interface{}{
			map[string]interface{}{"name": "candidate_list", "description": "候选框架列表"},
			map[string]interface{}{"name": "recommendation", "description": "推荐"},
		},
		"completion_criteria": []interface{}{"至少比较三个", "给出推荐"},
		"background":          map[string]interface{}{"project": "omnibot"},
	})
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))
	taskID := int64(parsed["task_id"].(float64))

	summary, err := svc.QueryTask(42, taskID)
	require.NoError(t, err)
	assert.Equal(t, "研究 Go 框架", summary.Goal)
}

// TestCreateDelegateTool_PassesPersonaHintAndType persona_hint/task_type 流进 taskSpec(任务级角色,非框架枚举)。
func TestCreateDelegateTool_PassesPersonaHintAndType(t *testing.T) {
	tool, svc := setupDelegateToolTest(t)
	ctx := withUserID(context.Background(), 7)
	result, err := tool.Execute(ctx, map[string]interface{}{
		"goal":         "调研三个部署方案",
		"persona_hint": "你是严谨的研究员,先多路检索再出结构化报告+来源",
		"task_type":    "research",
	})
	require.NoError(t, err)
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))
	task, err := svc.taskRepo.GetByID(int64(parsed["task_id"].(float64)))
	require.NoError(t, err)
	assert.Equal(t, "你是严谨的研究员,先多路检索再出结构化报告+来源", task.TaskSpec.PersonaHint)
	assert.Equal(t, "research", task.TaskSpec.Type)
	assert.Equal(t, "research", task.SubAgentType)
}

func TestCreateDelegateTool_ExecuteNoUserID(t *testing.T) {
	tool, _ := setupDelegateToolTest(t)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"goal": "g",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no user id")
}

func TestCreateDelegateTool_ExecuteMissingGoal(t *testing.T) {
	tool, _ := setupDelegateToolTest(t)
	ctx := withUserID(context.Background(), 1)
	_, err := tool.Execute(ctx, map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "required")
}

// 编译期:保证 taskSpec 字段名引用合法。
var _ = domainagent.TaskSpec{}
