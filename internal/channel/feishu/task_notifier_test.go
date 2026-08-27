package feishu

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
	agentpkg "omnibot/internal/service/agent"
)

// stubFeishuTask 构造一个飞书来源的已完成任务。
func stubFeishuTask() *domainagent.AgentTask {
	t := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("查询北京天气"), domainagent.SourceFeishu, "ou_test_open_id")
	art := "北京未来7天天气:晴,25-32度..."
	t.Artifact = &art
	t.Status = domainagent.TaskStatusCompleted
	t.ID = 99 // 手动设 ID(测试不经 DB,需显式赋值校验落库 task_id)
	return t
}

// TestFeishuNotifier_RunsAgentSavesAndPushes 主 Agent 转述成功:
// 落 SaveReportMessage(自然语言) + 推 SendMarkdown(自然语言,非裸回执)。
func TestFeishuNotifier_RunsAgentSavesAndPushes(t *testing.T) {
	sender := &mockSender{}
	agentSvc := &mockAgentService{result: &agentpkg.AgentResult{FinalResponse: "主人，北京未来7天都是晴天，25-32度，适合出行~"}}
	msgSvc := &mockMessageService{}

	n := &FeishuTaskNotifier{
		sender:   sender,
		agentSvc: agentSvc,
		msgSvc:   msgSvc,
		// llmConfigSvc nil:测试不选用户配置,走默认
	}

	err := n.NotifyTaskCompleted(context.Background(), "ou_test_open_id", stubFeishuTask())
	require.NoError(t, err)

	// 主 Agent Run 被调
	assert.Equal(t, 1, agentSvc.callCount, "应调用主 Agent Run 转述")
	// 落库:report message 内容=主 Agent 自然语言
	assert.True(t, msgSvc.reportCalled, "应落 SaveReportMessage")
	assert.Equal(t, "主人，北京未来7天都是晴天，25-32度，适合出行~", msgSvc.savedReportContent)
	assert.Equal(t, int64(99), msgSvc.savedReportTaskID)
	// 推送:走带标题的 2.0 卡片(SendCard),内容=自然语言(非裸回执"[子任务完成回执]")
	assert.Equal(t, "card", sender.lastMode, "应走 SendCard 卡片")
	assert.Equal(t, "📋 子任务完成汇报", sender.sentTitle, "卡片标题应为回执标题")
	assert.Equal(t, "blue", sender.sentTemplate)
	assert.Contains(t, sender.sentContent, "主人，北京未来7天")
	assert.NotContains(t, sender.sentContent, "[子任务完成回执]", "不应是裸回执格式")
	assert.NotContains(t, sender.sentContent, "📋 子任务完成汇报", "标题由 header 承载,正文不应含标题文本")
}

// TestFeishuNotifier_FallsBackToReceiptOnAgentError 主 Agent 转述失败:
// 回落裸回执推送(消息必达),不落库(无自然语言)。
func TestFeishuNotifier_FallsBackToReceiptOnAgentError(t *testing.T) {
	sender := &mockSender{}
	agentSvc := &mockAgentService{err: assert.AnError}
	msgSvc := &mockMessageService{}

	n := &FeishuTaskNotifier{
		sender:   sender,
		agentSvc: agentSvc,
		msgSvc:   msgSvc,
	}

	err := n.NotifyTaskCompleted(context.Background(), "ou_test_open_id", stubFeishuTask())
	require.NoError(t, err, "回落推送成功应返回 nil")

	// 走裸回执 fallback:带标题的 2.0 卡片,正文=裸回执(标题由 header 承载,正文不含前缀)
	assert.Equal(t, "card", sender.lastMode, "fallback 也应走 SendCard 卡片")
	assert.Equal(t, "📋 子任务完成汇报", sender.sentTitle)
	assert.Contains(t, sender.sentContent, "[子任务完成回执]", "回落正文应是裸回执")
	assert.NotContains(t, sender.sentContent, "📋 子任务完成汇报", "标题由 header 承载,正文不应含标题前缀")
	assert.Equal(t, "", msgSvc.savedReportContent, "回落不应落库")
	assert.False(t, msgSvc.reportCalled, "回落不应落库")
}
