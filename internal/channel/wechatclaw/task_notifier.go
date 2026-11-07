package wechatclaw

// task_notifier.go — 微信 ClawBot 的子任务完成推送(33 号 N2)。
//
// 与飞书的差异只在"怎么发":
//   - ClawBot 不支持卡片/markdown → 纯文本 SendText(超长由 Sender 自动分段);
//   - 连接是动态的(重扫/断线)→ 每次推送时用 provider 取当前 sender,不缓存实例。
//
// 转述与落库复用 agentpkg.ReportComposer,与飞书行为完全一致。

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	domainagent "omnibot/internal/domain/agent"
	agentpkg "omnibot/internal/service/agent"
	"omnibot/pkg/logger"
)

// reportTitle 微信侧标题(纯文本,卡片能力不可用)。
const reportTitle = "📋 子任务完成汇报"

// TextSender 只依赖发送能力(测试可注入 fake;*channelfeishu.Sender 满足)。
type TextSender interface {
	SendText(ctx context.Context, openID, content string) error
}

// SenderProvider 动态取当前发送器:断线/重扫后返回 nil 或新实例。
type SenderProvider func() TextSender

// TaskNotifier 微信 ClawBot 任务完成推送器。
type TaskNotifier struct {
	sender   SenderProvider
	composer *agentpkg.ReportComposer
}

// NewTaskNotifier 装配。composer 为 nil 时一律走裸回执(不 crash)。
func NewTaskNotifier(sender SenderProvider, composer *agentpkg.ReportComposer) *TaskNotifier {
	return &TaskNotifier{sender: sender, composer: composer}
}

// Available 当前是否有可用连接。SubAgentService 据此决定是否先标记 reported:
// 断线时不标记,保留"下次发消息时前置汇报"兜底。
func (n *TaskNotifier) Available() bool {
	if n == nil || n.sender == nil {
		return false
	}
	return n.sender() != nil
}

// NotifyTaskCompleted 转述 -> 落库 -> 发微信。
func (n *TaskNotifier) NotifyTaskCompleted(ctx context.Context, target string, task *domainagent.AgentTask) error {
	if n == nil || n.sender == nil {
		return fmt.Errorf("wechatclaw notifier: sender provider not configured")
	}
	sender := n.sender()
	if sender == nil {
		// 未连接(重扫中/断线):任务仍是 unreported,用户下次发消息会被前置汇报兜住。
		return fmt.Errorf("wechatclaw notifier: not connected")
	}

	content, records, ok := n.composer.Compose(ctx, task)
	if !ok {
		// 转述失败:裸回执保证消息必达(不落库)。
		return sender.SendText(ctx, target, reportTitle+"\n\n"+agentpkg.BuildTaskReceipt(task))
	}

	if err := n.composer.Save(ctx, task, content, records); err != nil {
		logger.ErrorWithFields("wechatclaw: save report message failed",
			zap.Int64("task_id", task.ID), zap.Error(err))
	}

	if err := sender.SendText(ctx, target, reportTitle+"\n\n"+content); err != nil {
		logger.ErrorWithFields("wechatclaw: notify task completed failed",
			zap.Int64("task_id", task.ID), zap.String("peer", target), zap.Error(err))
		return err
	}
	logger.InfoWithFields("wechatclaw: task completed notified",
		zap.Int64("task_id", task.ID), zap.String("peer", target))
	return nil
}
