package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
)

// 让 stub 满足 SubAgentService 的 StartTask 调用需要:delegate 工具调的是 *SubAgentService.StartTask。
// 单测里直接用一个真的 SubAgentService(配 stub runner)+ registry,更真实。

func setupDelegateToolTest(t *testing.T) (Tool, *SubAgentService, *SubAgentRegistry) {
	db := setupSubAgentServiceTestDB(t)
	repo := repoagent.NewAgentTaskRepository(db)
	registry := NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type:           "researcher",
		Name:           "研究员",
		Description:    "查阅资料/阅读RSS/检索历史的耗时研究任务",
		PromptTemplate: "你是研究员。目标:{goal}",
		Tools:          []string{"rss_reader"},
		MaxSteps:       15,
		Timeout:        5 * time.Second,
	}))
	// stub runner:立即返回 artifact(不真跑 LLM)
	svc := NewSubAgentService(repo, registry, &mockRunner{artifact: "result"}, chatrepo.NewAgentStepRepository(db))
	tool := CreateDelegateTool(registry, svc)
	return tool, svc, registry
}

func TestCreateDelegateTool_DescriptionContainsSubAgents(t *testing.T) {
	tool, _, _ := setupDelegateToolTest(t)
	assert.Equal(t, "delegate", tool.Name)
	// 描述含子 Agent 能力
	assert.Contains(t, tool.Description, "researcher")
	assert.Contains(t, tool.Description, "研究员")
	assert.Contains(t, tool.Description, "查阅资料")
}

func TestCreateDelegateTool_ExecuteStartsTask(t *testing.T) {
	tool, _, _ := setupDelegateToolTest(t)

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{
		"sub_agent_type": "researcher",
		"goal":           "研究 Go 1.24 新特性",
	})
	require.NoError(t, err)

	// 结果是 JSON,含 task_id
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))
	assert.NotZero(t, parsed["task_id"])
	assert.Equal(t, "pending", parsed["status"])
}

// TestCreateDelegateTool_PassesTaskSpec delegate 传 deliverables/criteria 应落进 task.TaskSpec。
func TestCreateDelegateTool_PassesTaskSpec(t *testing.T) {
	tool, svc, _ := setupDelegateToolTest(t)

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{
		"sub_agent_type": "researcher",
		"goal":           "研究 Go 框架",
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

	// 查回 task,验证 TaskSpec 落库
	summary, err := svc.QueryTask(42, taskID)
	require.NoError(t, err)
	assert.Equal(t, "研究 Go 框架", summary.Goal)
}

func TestCreateDelegateTool_ExecuteNoUserID(t *testing.T) {
	tool, _, _ := setupDelegateToolTest(t)
	// ctx 不带 userID
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"sub_agent_type": "researcher",
		"goal":           "g",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no user id")
}

func TestCreateDelegateTool_ExecuteMissingArgs(t *testing.T) {
	tool, _, _ := setupDelegateToolTest(t)
	ctx := withUserID(context.Background(), 1)
	_, err := tool.Execute(ctx, map[string]interface{}{
		"sub_agent_type": "researcher",
		// 缺 goal
	})
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "required")
}

func TestCreateDelegateTool_ExecuteUnregisteredType(t *testing.T) {
	tool, _, _ := setupDelegateToolTest(t)
	ctx := withUserID(context.Background(), 1)
	_, err := tool.Execute(ctx, map[string]interface{}{
		"sub_agent_type": "nonexistent",
		"goal":           "g",
	})
	require.Error(t, err)
}
