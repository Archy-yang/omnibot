package agent

import (
	"context"

	domainagent "omnibot/internal/domain/agent"
)

// TaskNotifier 任务完成时的主动推送器(方案A:飞书主动消息)。
//
// 解决问题:飞书派来的任务,子 Agent 完成后若用户不再发消息,C 模式(延迟汇报)永远不触发,
// 结果躺在 DB 里。本接口让 executeTask 完成时主动把结果推回来源渠道(飞书=open_id)。
//
// 解耦:SubAgentService(执行层)不直接依赖飞书 sender,通过此接口。
// routes.go 注入 FeishuTaskNotifier(包装飞书 Sender);web 任务 source=web 时 notifier 为 nil(靠轮询)。
type TaskNotifier interface {
	// NotifyTaskCompleted 任务完成时推送汇报。target=推送目标(feishu=open_id)。
	// 返回 error 仅记日志,不影响任务状态(推送失败不阻断,reported 由调用方控制)。
	NotifyTaskCompleted(ctx context.Context, target string, task *domainagent.AgentTask) error
}

// noopTaskNotifier 空实现(未注入或 web 任务时用,不推送)。
type noopTaskNotifier struct{}

func (noopTaskNotifier) NotifyTaskCompleted(ctx context.Context, target string, task *domainagent.AgentTask) error {
	return nil
}
