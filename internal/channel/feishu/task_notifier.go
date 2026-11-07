package feishu

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	domainagent "omnibot/internal/domain/agent"
	agentpkg "omnibot/internal/service/agent"
	"omnibot/pkg/logger"
)

// 回执推送的卡片标题与主题色(JSON 2.0 卡片 header)。
// 转述成功(自然语言正文)与 fallback(裸回执正文)共用同一标题,统一视觉。
const (
	reportCardTitle    = "📋 子任务完成汇报"
	reportCardTemplate = "blue"
)

// FeishuTaskNotifier 飞书任务完成主动推送(方案A / 33 号 N1)。
//
// 转述与落库已抽到 agentpkg.ReportComposer(飞书 / 微信 ClawBot 共用),
// 本类型只剩渠道差异:用带标题的卡片把自然语言正文推给 open_id。
//
// 转述失败回落裸回执:保证消息必达(不因 LLM 失败而丢消息),但回落不落库(无自然语言)。
type FeishuTaskNotifier struct {
	sender   Sender
	composer *agentpkg.ReportComposer
}

// NewFeishuTaskNotifier 创建飞书任务推送器。去角色后不接收 SubAgentRegistry。
func NewFeishuTaskNotifier(
	sender Sender,
	agentSvc AgentService,
	msgSvc MessageService,
	llmConfigSvc LLMConfigService,
) *FeishuTaskNotifier {
	return &FeishuTaskNotifier{
		sender:   sender,
		composer: agentpkg.NewReportComposer(agentSvc, msgSvc, msgSvc, feishuReportLLM{svc: llmConfigSvc}),
	}
}

// NotifyTaskCompleted 走主 Agent 转述 -> 落库 -> 推送飞书卡片。
// 转述失败回落裸回执推送(消息必达)。
func (n *FeishuTaskNotifier) NotifyTaskCompleted(ctx context.Context, openID string, task *domainagent.AgentTask) error {
	if n.sender == nil {
		return fmt.Errorf("feishu notifier: sender not configured")
	}

	finalResponse, records, ok := n.composer.Compose(ctx, task)
	if !ok {
		// 转述失败:回落裸回执推送(消息必达),不落库
		return n.sendReceiptFallback(ctx, openID, task)
	}

	// 落 report message(Kind=report,关联 task_id):刷新后历史仍能还原汇报。
	// 落库失败仅记日志,仍推送(消息能到用户最重要)。
	if err := n.composer.Save(ctx, task, finalResponse, records); err != nil {
		logger.ErrorWithFields("feishu: save report message failed",
			zap.Int64("task_id", task.ID), zap.Error(err))
	}

	// 推送飞书(主 Agent 自然语言),用带标题的 2.0 卡片。
	if err := n.sender.SendCard(ctx, openID, reportCardTitle, finalResponse, reportCardTemplate); err != nil {
		logger.ErrorWithFields("feishu: notify task completed failed",
			zap.Int64("task_id", task.ID), zap.String("open_id", openID), zap.Error(err))
		return err
	}
	logger.InfoWithFields("feishu: task completed notified",
		zap.Int64("task_id", task.ID), zap.String("open_id", openID))
	return nil
}

// sendReceiptFallback 转述失败时推裸回执(消息必达)。不落库。
// 用带标题的 2.0 卡片:标题由 header 承载,正文直接用 receipt(不再加文本前缀)。
func (n *FeishuTaskNotifier) sendReceiptFallback(ctx context.Context, openID string, task *domainagent.AgentTask) error {
	receipt := agentpkg.BuildTaskReceipt(task)
	if err := n.sender.SendCard(ctx, openID, reportCardTitle, receipt, reportCardTemplate); err != nil {
		logger.ErrorWithFields("feishu: receipt fallback send failed",
			zap.Int64("task_id", task.ID), zap.String("open_id", openID), zap.Error(err))
		return err
	}
	return nil
}

// feishuReportLLM 适配渠道侧 LLMConfigService 到汇报组件的窄接口。
// nil 安全:未装配时返回 hasConfig=false,转述用系统默认模型。
type feishuReportLLM struct {
	svc LLMConfigService
}

func (a feishuReportLLM) GetFullConfig(userID int64) (apiKey, baseURL, model string, hasConfig bool, err error) {
	if a.svc == nil {
		return "", "", "", false, nil
	}
	cfg, has, e := a.svc.GetFullConfigForUser(userID)
	if e != nil || !has || cfg == nil {
		return "", "", "", false, e
	}
	return cfg.APIKey, cfg.BaseURL, cfg.Model, true, nil
}

// SendPlanText 计划异常消息(34 号):纯文本一行,不必卡片。
func (n *FeishuTaskNotifier) SendPlanText(ctx context.Context, openID, text string) error {
	if n.sender == nil {
		return fmt.Errorf("feishu notifier: sender not configured")
	}
	return n.sender.SendText(ctx, openID, text)
}
