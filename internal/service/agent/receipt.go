package agent

import (
	"fmt"
	"strings"

	domainagent "omnibot/internal/domain/agent"
)

// BuildTaskReceipt 构造子任务回执(08 §3.4 固定格式),供主 Agent 识别并汇报。
//
// completed 任务:结果 = Artifact
// failed 任务:结果 = 失败原因(ErrorMsg,已脱敏)
//
// 主 Agent system 指示:看到此格式先向用户汇报该任务结果,再处理当前消息。
// web(report 接口/前置汇报)和飞书(前置汇报)共用此格式。
func BuildTaskReceipt(registry *SubAgentRegistry, task *domainagent.AgentTask) string {
	var name string
	if card, ok := registry.Get(task.SubAgentType); ok {
		name = card.Name
	} else {
		name = task.SubAgentType // 回落到类型标识
	}

	result := ""
	if task.Status == domainagent.TaskStatusFailed {
		errMsg := "未知错误"
		if task.ErrorMsg != nil {
			errMsg = *task.ErrorMsg
		}
		result = fmt.Sprintf("失败: %s", errMsg)
	} else if task.Artifact != nil {
		result = *task.Artifact
	}

	var b strings.Builder
	b.WriteString("[子任务完成回执]\n")
	fmt.Fprintf(&b, "任务ID: %d\n", task.ID)
	fmt.Fprintf(&b, "子Agent: %s(%s)\n", name, task.SubAgentType)
	fmt.Fprintf(&b, "目标: %s\n", task.Goal)
	b.WriteString("结果:\n")
	b.WriteString(result)
	return b.String()
}

// BuildReportInstruction 构造汇报指令 system 消息内容:回执 + 主 Agent 行为指令。
// 用于 report 接口(单任务汇报)和前置汇报(多任务)。
func BuildReportInstruction(registry *SubAgentRegistry, tasks []*domainagent.AgentTask) string {
	var b strings.Builder
	b.WriteString("以下是你之前安排的子任务的完成回执,请向用户汇报这些任务的结果,")
	b.WriteString("用管家口吻转述(不要原样照搬回执格式,要自然语言汇报)。")
	b.WriteString("汇报完即可,不需要用户再追问。\n\n")
	for _, task := range tasks {
		b.WriteString(BuildTaskReceipt(registry, task))
		b.WriteString("\n---\n")
	}
	return b.String()
}
