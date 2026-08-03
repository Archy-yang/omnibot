package feishu

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	domainagent "omnibot/internal/domain/agent"
	agentpkg "omnibot/internal/service/agent"
	"omnibot/pkg/logger"
)

// FeishuTaskNotifier 飞书任务完成主动推送(方案A)。
//
// 实现 agent.TaskNotifier 接口:子 Agent 完成时,把结果摘要主动推送到飞书(open_id)。
// 解决飞书派来任务后用户不再发消息导致结果躺在 DB 的 C 模式缺口。
//
// 推送内容:用 BuildTaskReceipt 构造回执摘要(控制面分离,不塞全文),走 SendMarkdown 渲染。
type FeishuTaskNotifier struct {
	sender    Sender
	registry  *agentpkg.SubAgentRegistry
}

// NewFeishuTaskNotifier 创建飞书任务推送器。
func NewFeishuTaskNotifier(sender Sender, registry *agentpkg.SubAgentRegistry) *FeishuTaskNotifier {
	return &FeishuTaskNotifier{sender: sender, registry: registry}
}

// NotifyTaskCompleted 把任务结果推送到飞书 open_id。
// 用 BuildTaskReceipt 构造摘要回执(控制面分离),SendMarkdown 渲染。
func (n *FeishuTaskNotifier) NotifyTaskCompleted(ctx context.Context, openID string, task *domainagent.AgentTask) error {
	if n.sender == nil {
		return fmt.Errorf("feishu notifier: sender not configured")
	}
	receipt := agentpkg.BuildTaskReceipt(n.registry, task)
	// 加引导语,让用户知道这是主动汇报
	msg := "📋 子任务完成汇报\n\n" + receipt
	if err := n.sender.SendMarkdown(ctx, openID, msg); err != nil {
		logger.ErrorWithFields("feishu: notify task completed failed",
			zap.Int64("task_id", task.ID), zap.String("open_id", openID), zap.Error(err))
		return err
	}
	logger.InfoWithFields("feishu: task completed notified",
		zap.Int64("task_id", task.ID), zap.String("open_id", openID))
	return nil
}
