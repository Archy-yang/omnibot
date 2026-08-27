package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
)

// mockTaskNotifier 记录是否被调用 + 参数。
type mockTaskNotifier struct {
	called   bool
	target   string
	taskID   int64
}

func (m *mockTaskNotifier) NotifyTaskCompleted(ctx context.Context, target string, task *domainagent.AgentTask) error {
	m.called = true
	m.target = target
	m.taskID = task.ID
	return nil
}

// TestNotifyCompleted_Feishu 飞书任务完成时调 notifier 推送 + 标记 reported。
func TestNotifyCompleted_Feishu(t *testing.T) {
	notifier := &mockTaskNotifier{}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "结果", delay: 20*time.Millisecond}, true)
	svc.SetNotifier(notifier)

	// 手动造一个飞书来源的 running 任务
	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceFeishu, "ou_openid_xxx")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusRunning, nil, nil))

	// 调 notifyCompleted(模拟 executeTask 完成)
	svc.notifyCompleted(task.ID)

	require.True(t, notifier.called, "飞书任务应触发 notifier")
	assert.Equal(t, "ou_openid_xxx", notifier.target)
	assert.Equal(t, task.ID, notifier.taskID)
	// 应标记 reported(防前置汇报重复)
	got, _ := repo.GetByID(task.ID)
	assert.True(t, got.Reported, "推送后应标记 reported")
}

// TestNotifyCompleted_Web web 任务不推送(靠轮询)。
func TestNotifyCompleted_Web(t *testing.T) {
	notifier := &mockTaskNotifier{}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "r", delay: 20*time.Millisecond}, true)
	svc.SetNotifier(notifier)

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceWeb, "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusRunning, nil, nil))

	svc.notifyCompleted(task.ID)
	assert.False(t, notifier.called, "web 任务不应触发 notifier")
}
