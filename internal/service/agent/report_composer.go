package agent

// report_composer.go — 子任务汇报的「转述 + 落库」共用组件(33 号 N1)。
//
// 背景:飞书 notifier 里原本内联了「组装汇报上下文 → 主 Agent 转述 → 落 report 消息」,
// 微信 ClawBot 接入后需要同一段逻辑。抽到 service 层,渠道只保留"怎么发"的差异:
//
//	飞书   → SendCard(带标题卡片)
//	微信   → SendText(纯文本,超长自动分段)
//
// 渠道不负责转述,也不负责落库——避免两处各写一遍导致行为漂移。

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"omnibot/internal/client/llm"
	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"
	"omnibot/pkg/logger"
)

// reportLLMTimeout 转述请求超时(与飞书 notifier 原实现保持一致)。
const reportLLMTimeout = 30 * time.Second

// ReportRunner 主 Agent 同步执行入口(*AgentService 实现)。
type ReportRunner interface {
	Run(ctx context.Context, userID int64, conversation []map[string]interface{}, customLLMClient ...LLMClient) (*AgentResult, error)
}

// ReportContextSource 对话历史来源(锚定原始问题,防止汇报脱离语境)。
type ReportContextSource interface {
	BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error)
}

// ReportMessageStore 汇报消息落库(Kind=report,关联 task_id)。
type ReportMessageStore interface {
	SaveReportMessage(ctx context.Context, userID, taskID, turnID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error
}

// ReportComposer 渠道无关的汇报组件。
type ReportComposer struct {
	runner        ReportRunner
	contextSource ReportContextSource
	store         ReportMessageStore
	llm           SubAgentLLMConfigProvider
}

// NewReportComposer 装配。llm 可为 nil(转述用系统默认模型)。
func NewReportComposer(runner ReportRunner, ctxSource ReportContextSource, store ReportMessageStore, llm SubAgentLLMConfigProvider) *ReportComposer {
	return &ReportComposer{runner: runner, contextSource: ctxSource, store: store, llm: llm}
}

// Compose 组装汇报上下文(回执指令 + 对话历史 + 虚拟触发)并让主 Agent 转述。
// ok=false 表示转述失败(调用方回落裸回执,保证消息必达)。
func (c *ReportComposer) Compose(ctx context.Context, task *domainagent.AgentTask) (string, []StepRecord, bool) {
	if c == nil || c.runner == nil || task == nil {
		return "", nil, false
	}

	// 汇报锚定(08 §3.4 修订):system(回执+指令) + 最近对话历史(锚定原始问题) + 虚拟触发。
	instruction := BuildReportInstruction([]*domainagent.AgentTask{task}, true)
	conv := []map[string]interface{}{
		{"role": "system", "content": instruction},
	}
	const trigger = "请汇报这个子任务的结果。"

	if c.contextSource != nil {
		if ctxMsgs, err := c.contextSource.BuildContextMessages(ctx, task.UserID, trigger); err == nil {
			conv = append(conv, ChatMessagesToAgent(ctxMsgs)...)
		} else {
			logger.WarnWithFields("report: 拉取对话历史失败,降级纯回执汇报",
				zap.Int64("task_id", task.ID), zap.Error(err))
			conv = append(conv, map[string]interface{}{"role": "user", "content": trigger})
		}
	} else {
		conv = append(conv, map[string]interface{}{"role": "user", "content": trigger})
	}

	// 选 LLM:用户自定义优先(与主对话一致);未配置则不传,由 Agent 用系统默认。
	var customClient LLMClient
	if c.llm != nil {
		if apiKey, baseURL, model, has, err := c.llm.GetFullConfig(task.UserID); err == nil && has && apiKey != "" {
			customClient = NewOpenAILLMClient(apiKey, baseURL, model, reportLLMTimeout)
		}
	}

	result, err := c.runner.Run(ctx, task.UserID, conv, customClient)
	if err != nil {
		logger.ErrorWithFields("report: 主 Agent 转述失败,回落裸回执",
			zap.Int64("task_id", task.ID), zap.Error(err))
		return "", nil, false
	}
	if result == nil || strings.TrimSpace(result.FinalResponse) == "" {
		logger.WarnWithFields("report: 主 Agent 返回空汇报,回落裸回执",
			zap.Int64("task_id", task.ID))
		return "", nil, false
	}
	return result.FinalResponse, result.Records, true
}

// Save 把转述结果落成 report 消息。落库失败只记日志并返回错误(调用方决定是否阻断推送)。
func (c *ReportComposer) Save(ctx context.Context, task *domainagent.AgentTask, content string, records []StepRecord) error {
	if c == nil || c.store == nil || task == nil {
		return nil
	}
	var turnID int64
	if task.OriginTurnID != nil {
		turnID = *task.OriginTurnID // report 逻辑归属任务发起时的 Turn(可晚于后续 Turn,§5.5)
	}
	steps := StepRecordsToAgentSteps(records, task.UserID, "")
	if err := c.store.SaveReportMessage(ctx, task.UserID, task.ID, turnID, content, nil, steps); err != nil {
		logger.ErrorWithFields("report: 落库汇报消息失败",
			zap.Int64("task_id", task.ID), zap.Error(err))
		return err
	}
	return nil
}

// ChatMessagesToAgent 把 llm.ChatMessage 转成 Agent 的通用 conversation 结构。
// 下沉到本包(而非留在 channel/im),让汇报组件不必反向依赖渠道层;
// channel/im.ToAgentMessages 改为委托本函数,对外 API 不变。
func ChatMessagesToAgent(messages []llm.ChatMessage) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		m := map[string]interface{}{"role": msg.Role, "content": msg.Content}
		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
		}
		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		items = append(items, m)
	}
	return items
}
