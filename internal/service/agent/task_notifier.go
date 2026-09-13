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

// TaskCompletionPublisher web 任务完成时向该用户在线连接推送实时事件(08 §4.8)。
// realtime.Hub 实现;为 nil 时退化纯轮询(兼容老路径/测试)。与 TaskNotifier(飞书
// 主动消息)并行:按 task.Source 分派,web 推 WS 事件、飞书走主动消息。
type TaskCompletionPublisher interface {
	// PublishTaskCompleted 推送 {"type":"task.completed","data":{"task_id":N}}。
	// 只送通知不送内容——汇报正文仍由前端触发 /report SSE 链路生成并落库。
	PublishTaskCompleted(userID, taskID int64)
}
