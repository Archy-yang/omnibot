package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
)

// TestLocalAgentExecutor_AgentID 标识为 "local"。
func TestLocalAgentExecutor_AgentID(t *testing.T) {
	e := NewLocalAgentExecutor(nil)
	assert.Equal(t, "local", e.AgentID())
}

// TestLocalAgentExecutor_Capabilities 报告支持取消/输入。
func TestLocalAgentExecutor_Capabilities(t *testing.T) {
	e := NewLocalAgentExecutor(nil)
	cap, err := e.Capabilities(context.Background())
	require.NoError(t, err)
	assert.True(t, cap.SupportsCancellation)
	assert.True(t, cap.SupportsInputRequired)
}

// TestLocalAgentExecutor_Submit 提交任务返回回执(委托 svc.StartTask)。
func TestLocalAgentExecutor_Submit(t *testing.T) {
	svc, _, _ := setupSubAgentService(t, &mockRunner{artifact: "r"})
	e := NewLocalAgentExecutor(svc)
	ctx := withUserID(context.Background(), 42)

	receipt, err := e.Submit(ctx, 42, "researcher", domainagent.NewTaskSpec("g"))
	require.NoError(t, err)
	assert.NotZero(t, receipt.TaskID)
	assert.Equal(t, domainagent.TaskStatusPending, receipt.Status)
}

// TestLocalAgentExecutor_Send 发消息作为 note 补充(委托 UpdateTask)。
func TestLocalAgentExecutor_Send(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	e := NewLocalAgentExecutor(svc)
	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusRunning, nil, nil))

	ctx := withUserID(context.Background(), 42)
	err := e.Send(ctx, task.ID, AgentMessage{
		Role: "parent_agent",
		Parts: []MessagePart{{Type: "text", Text: "补充信息"}},
	})
	require.NoError(t, err)

	got, _ := repo.GetByID(task.ID)
	require.Len(t, got.Notes, 1)
	assert.Equal(t, "补充信息", got.Notes[0])
}

// TestLocalAgentExecutor_Status 查任务状态。
func TestLocalAgentExecutor_Status(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	e := NewLocalAgentExecutor(svc)
	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))

	st, err := e.Status(context.Background(), task.ID)
	require.NoError(t, err)
	assert.Equal(t, task.ID, st.TaskID)
	assert.Equal(t, domainagent.TaskStatusPending, st.Status)
}

// TestLocalAgentExecutor_Cancel 取消任务(委托 CancelTask,需 userID)。
func TestLocalAgentExecutor_Cancel(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	e := NewLocalAgentExecutor(svc)
	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))

	ctx := withUserID(context.Background(), 42)
	require.NoError(t, e.Cancel(ctx, task.ID))
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domainagent.TaskStatusCancelled, got.Status)
}

// TestLocalAgentExecutor_Send_NoUserID 主 Agent ctx 无 userID 报错。
func TestLocalAgentExecutor_Send_NoUserID(t *testing.T) {
	svc, _, _ := setupSubAgentService(t, &mockRunner{})
	e := NewLocalAgentExecutor(svc)
	err := e.Send(context.Background(), 1, AgentMessage{Parts: []MessagePart{{Type: "text", Text: "x"}}})
	require.Error(t, err)
}
