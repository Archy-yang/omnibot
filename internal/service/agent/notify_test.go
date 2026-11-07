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
	called bool
	target string
	taskID int64
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
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "结果", delay: 20 * time.Millisecond}, true)
	svc.SetNotifierForSource(domainagent.SourceFeishu, notifier)

	// 手动造一个飞书来源的 running 任务
	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceFeishu, "ou_openid_xxx")
	require.NoError(t, repo.Create(task))
	mustTransitionSvc(t, repo, task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

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
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "r", delay: 20 * time.Millisecond}, true)
	svc.SetNotifierForSource(domainagent.SourceFeishu, notifier)

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceWeb, "")
	require.NoError(t, repo.Create(task))
	mustTransitionSvc(t, repo, task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

	svc.notifyCompleted(task.ID)
	assert.False(t, notifier.called, "web 任务不应触发 notifier")
}

// mockCompletionPublisher 记录 web 实时推送调用(08 §4.8)。
type mockCompletionPublisher struct {
	userID int64
	taskID int64
	called bool
}

func (m *mockCompletionPublisher) PublishTaskCompleted(userID, taskID int64) {
	m.called = true
	m.userID = userID
	m.taskID = taskID
}

// TestNotifyCompleted_Web_Publisher web 任务完成时向 WS 推送事件,且不标 reported
// (reported 由 /report 处理;推送丢失靠轮询兜底)。
func TestNotifyCompleted_Web_Publisher(t *testing.T) {
	pub := &mockCompletionPublisher{}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "r", delay: 20 * time.Millisecond}, true)
	svc.SetCompletionPublisher(pub)

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceWeb, "")
	require.NoError(t, repo.Create(task))
	mustTransitionSvc(t, repo, task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

	svc.notifyCompleted(task.ID)
	assert.True(t, pub.called, "web 任务完成应推送 WS 事件")
	assert.Equal(t, int64(42), pub.userID)
	assert.Equal(t, task.ID, pub.taskID)

	updated, _ := repo.GetByID(task.ID)
	assert.NotNil(t, updated)
}

// TestNotifyCompleted_Feishu_NoPublisher 飞书任务不走 web 推送。
func TestNotifyCompleted_Feishu_NoPublisher(t *testing.T) {
	pub := &mockCompletionPublisher{}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "r", delay: 20 * time.Millisecond}, true)
	svc.SetCompletionPublisher(pub)

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceFeishu, "ou_xxx")
	require.NoError(t, repo.Create(task))
	mustTransitionSvc(t, repo, task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

	svc.notifyCompleted(task.ID)
	assert.False(t, pub.called, "飞书任务不应触发 WS 推送")
}

// mockAvailableNotifier 带可用性开关(33 号:微信断线/重扫中)。
type mockAvailableNotifier struct {
	mockTaskNotifier
	available bool
}

func (m *mockAvailableNotifier) Available() bool { return m.available }

// TestNotifyCompleted_WeChatClaw 微信任务走微信 notifier,且与飞书互不串台。
func TestNotifyCompleted_WeChatClaw(t *testing.T) {
	feishuNotifier := &mockTaskNotifier{}
	clawNotifier := &mockTaskNotifier{}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "结果", delay: 20 * time.Millisecond}, true)
	svc.SetNotifierForSource(domainagent.SourceFeishu, feishuNotifier)
	svc.SetNotifierForSource(domainagent.SourceWeChatClaw, clawNotifier)

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceWeChatClaw, "wx_peer_1")
	require.NoError(t, repo.Create(task))
	mustTransitionSvc(t, repo, task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

	svc.notifyCompleted(task.ID)

	require.True(t, clawNotifier.called, "微信任务应触发微信 notifier")
	assert.Equal(t, "wx_peer_1", clawNotifier.target)
	assert.False(t, feishuNotifier.called, "不能把微信任务推到飞书")

	got, _ := repo.GetByID(task.ID)
	assert.True(t, got.Reported, "推送后应标记 reported")
}

// 微信未连接:不推、也不标记 reported(保留前置汇报兜底)。
func TestNotifyCompleted_WeChatClaw_UnavailableKeepsUnreported(t *testing.T) {
	clawNotifier := &mockAvailableNotifier{available: false}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "结果", delay: 20 * time.Millisecond}, true)
	svc.SetNotifierForSource(domainagent.SourceWeChatClaw, clawNotifier)

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceWeChatClaw, "wx_peer_1")
	require.NoError(t, repo.Create(task))
	mustTransitionSvc(t, repo, task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

	svc.notifyCompleted(task.ID)

	assert.False(t, clawNotifier.called, "断线时不推送")
	got, _ := repo.GetByID(task.ID)
	assert.False(t, got.Reported, "断线时不能标记已汇报,否则下次消息的兜底汇报也没了")
}

// 该来源没注册 notifier:不推送、不标记(留给兜底)。
func TestNotifyCompleted_NoNotifierForSource(t *testing.T) {
	clawNotifier := &mockTaskNotifier{}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "结果", delay: 20 * time.Millisecond}, true)
	svc.SetNotifierForSource(domainagent.SourceWeChatClaw, clawNotifier)

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceFeishu, "ou_x")
	require.NoError(t, repo.Create(task))
	mustTransitionSvc(t, repo, task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

	svc.notifyCompleted(task.ID)

	assert.False(t, clawNotifier.called)
	got, _ := repo.GetByID(task.ID)
	assert.False(t, got.Reported)
}
