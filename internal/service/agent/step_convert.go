package agent

import (
	"omnibot/internal/domain/conversation"
)

// StepRecordsToAgentSteps 把 agent 聚合产出的 StepRecord 链转成可落库的 conversation.AgentStep 链。
// web/feishu 主对话 + 子 Agent 后台任务共用此转换(去重,原 web/feishu 各一份)。
//
// 主 Agent 步骤:调用方设 MessageID(TaskID 留 nil)。
// 子 Agent 步骤:调用方设 TaskID(MessageID 留 nil)。
// MessageID/TaskID 由调用方在落库前 stamp,本函数只转 Kind/Request/Response/Status/Duration/Tool/Model + Seq。
func StepRecordsToAgentSteps(records []StepRecord, userID int64, model string) []*conversation.AgentStep {
	if len(records) == 0 {
		return nil
	}
	steps := make([]*conversation.AgentStep, 0, len(records))
	for i, r := range records {
		var step *conversation.AgentStep
		switch r.Kind {
		case StepKindLLMCall:
			step = conversation.NewLLMStep(userID, r.Request, r.Response, model, r.Status, r.DurationMs)
		case StepKindToolCall:
			step = conversation.NewToolStep(userID, r.Tool, r.Request, r.Response, r.Status, r.DurationMs)
		default:
			continue // 未知 kind 跳过,防御未来扩展
		}
		step.Seq = i
		steps = append(steps, step)
	}
	return steps
}
