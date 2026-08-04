package feishu

import (
	"context"
	"fmt"
	"time"

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

// FeishuTaskNotifier 飞书任务完成主动推送(方案A)。
//
// 实现 agent.TaskNotifier 接口:子 Agent 完成时,走主 Agent 转述成自然语言,
// 落 report message(Kind=report,关联 task_id) + 主动推送到飞书(open_id)。
//
// 解决两个问题:
//   - 落库:之前只推送不落库,飞书 task 结果永久丢失(MarkReported 后前置汇报也跳过)
//   - 自然语言:之前直接推 BuildTaskReceipt 裸回执格式,不友好。现走主 Agent Run 转述
//
// 转述失败回落裸回执:保证消息必达(不因 LLM 失败而丢消息),但回落不落库(无自然语言)。
type FeishuTaskNotifier struct {
	sender       Sender
	registry     *agentpkg.SubAgentRegistry
	agentSvc     AgentService     // 主 Agent 服务:Run 转述成自然语言
	msgSvc       MessageService   // 落 report message
	llmConfigSvc LLMConfigService // 选 LLM(用户自定义优先,与主对话一致)
}

// NewFeishuTaskNotifier 创建飞书任务推送器。
func NewFeishuTaskNotifier(
	sender Sender,
	registry *agentpkg.SubAgentRegistry,
	agentSvc AgentService,
	msgSvc MessageService,
	llmConfigSvc LLMConfigService,
) *FeishuTaskNotifier {
	return &FeishuTaskNotifier{
		sender:       sender,
		registry:     registry,
		agentSvc:     agentSvc,
		msgSvc:       msgSvc,
		llmConfigSvc: llmConfigSvc,
	}
}

// NotifyTaskCompleted 走主 Agent 转述 -> 落库 -> 推送飞书。
// 转述失败回落裸回执推送(消息必达)。
func (n *FeishuTaskNotifier) NotifyTaskCompleted(ctx context.Context, openID string, task *domainagent.AgentTask) error {
	if n.sender == nil {
		return fmt.Errorf("feishu notifier: sender not configured")
	}

	finalResponse, records, ok := n.runAgentReport(ctx, task)
	if !ok {
		// 转述失败:回落裸回执推送(消息必达),不落库
		return n.sendReceiptFallback(ctx, openID, task)
	}

	// 落 report message(Kind=report,关联 task_id):刷新后历史仍能还原汇报。
	// 落库失败仅记日志,仍推送(消息能到用户最重要)。
	steps := recordsToAgentSteps(records, task.UserID, "")
	if err := n.msgSvc.SaveReportMessage(ctx, task.UserID, task.ID, finalResponse, nil, steps); err != nil {
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

// runAgentReport 调主 Agent Run 把任务回执转述成自然语言。
// ok=false 表示转述失败(Run 报错或空回复)。
func (n *FeishuTaskNotifier) runAgentReport(ctx context.Context, task *domainagent.AgentTask) (finalResponse string, records []agentpkg.StepRecord, ok bool) {
	// 构造汇报上下文:system(回执+汇报指令,fullArtifact=true 给完整产物) + user(虚拟触发)
	instruction := agentpkg.BuildReportInstruction(n.registry, []*domainagent.AgentTask{task}, true)
	conv := []map[string]interface{}{
		{"role": "system", "content": instruction},
		{"role": "user", "content": "请汇报这个子任务的结果。"},
	}

	// 选 LLM:用户自定义优先(与主对话一致)
	var customClient agentpkg.LLMClient
	if n.llmConfigSvc != nil {
		if cfg, has, err := n.llmConfigSvc.GetFullConfigForUser(task.UserID); err == nil && has && cfg != nil {
			customClient = agentpkg.NewOpenAILLMClient(cfg.APIKey, cfg.BaseURL, cfg.Model, 30*time.Second)
		}
	}

	result, err := n.agentSvc.Run(ctx, task.UserID, conv, customClient)
	if err != nil || result == nil || result.FinalResponse == "" {
		if err != nil {
			logger.ErrorWithFields("feishu: agent report run failed, fallback to receipt",
				zap.Int64("task_id", task.ID), zap.Error(err))
		}
		return "", nil, false
	}
	return result.FinalResponse, result.Records, true
}

// sendReceiptFallback 转述失败时推裸回执(消息必达)。不落库。
// 用带标题的 2.0 卡片:标题由 header 承载,正文直接用 receipt(不再加文本前缀)。
func (n *FeishuTaskNotifier) sendReceiptFallback(ctx context.Context, openID string, task *domainagent.AgentTask) error {
	receipt := agentpkg.BuildTaskReceipt(n.registry, task)
	if err := n.sender.SendCard(ctx, openID, reportCardTitle, receipt, reportCardTemplate); err != nil {
		logger.ErrorWithFields("feishu: receipt fallback send failed",
			zap.Int64("task_id", task.ID), zap.String("open_id", openID), zap.Error(err))
		return err
	}
	return nil
}
