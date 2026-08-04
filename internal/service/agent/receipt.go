package agent

import (
	"fmt"
	"strings"

	domainagent "omnibot/internal/domain/agent"
)

// BuildTaskReceipt 构造子任务回执(08 §3.4 固定格式),供主 Agent 识别并汇报。
//
// 控制面/数据面分离(#20):回执只给结果摘要(前 receiptSummaryMax 字符)+ artifact_id 提示,
// 不塞全文(大内容走 agent_artifacts 表,主 Agent 需要详情时 GetTaskArtifact 按需读)。
//
// completed 任务:摘要 = Artifact 前 N 字
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
	} else if task.Status == domainagent.TaskStatusInputRequired {
		// input_required:把[需要输入]问题转述
		result = "等待输入: " + extractInputQuestion(task.Notes)
	} else if task.Artifact != nil {
		// completed:控制面只给摘要,不塞全文(数据面走 agent_artifacts 按需读)
		result = truncateForReceipt(*task.Artifact) + "\n(完整结果可查任务产物)"
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

// receiptSummaryMax 回执结果摘要最大字符数(控制面小消息,大内容走数据面)。
const receiptSummaryMax = 200

// truncateForReceipt 截断到摘要长度,超长加省略号。
func truncateForReceipt(s string) string {
	if len(s) <= receiptSummaryMax {
		return s
	}
	return s[:receiptSummaryMax] + "..."
}

// extractInputQuestion 从 Notes 提取[需要输入]问题(input_required 态用)。
func extractInputQuestion(notes []string) string {
	for i := len(notes) - 1; i >= 0; i-- {
		if n := notes[i]; strings.HasPrefix(n, "[需要输入] ") {
			return strings.TrimPrefix(n, "[需要输入] ")
		}
	}
	if len(notes) > 0 {
		return notes[len(notes)-1]
	}
	return "无"
}

// BuildReportInstruction 构造汇报指令 system 消息内容:回执 + 主 Agent 行为指令。
// 用于 report 接口(单任务详细汇报)和前置汇报(多任务转述)。
// report 接口需完整内容生成汇报 -> fullArtifact=true 给全文;
// 前置汇报转述多任务 -> fullArtifact=false 给摘要(控制面/数据面分离,#20),
//   主 Agent 需要详情再 query_task 取 artifact。
func BuildReportInstruction(registry *SubAgentRegistry, tasks []*domainagent.AgentTask, fullArtifact bool) string {
	var b strings.Builder
	b.WriteString("以下是你之前安排的子任务的完成回执,请向用户汇报这些任务的结果,")
	b.WriteString("用管家口吻转述(不要原样照搬回执格式,要自然语言汇报)。")
	b.WriteString("汇报完即可,不需要用户再追问。\n\n")
	for _, task := range tasks {
		b.WriteString(buildTaskReceiptForReport(registry, task, fullArtifact))
		b.WriteString("\n---\n")
	}
	return b.String()
}

// buildTaskReceiptForReport 按 fullArtifact 决定给全文或摘要。
func buildTaskReceiptForReport(registry *SubAgentRegistry, task *domainagent.AgentTask, fullArtifact bool) string {
	if !fullArtifact {
		return BuildTaskReceipt(registry, task) // 摘要(控制面)
	}
	// fullArtifact=true:给完整 artifact(report 接口详细汇报用)
	var name string
	if card, ok := registry.Get(task.SubAgentType); ok {
		name = card.Name
	} else {
		name = task.SubAgentType
	}
	result := ""
	if task.Status == domainagent.TaskStatusFailed {
		errMsg := "未知错误"
		if task.ErrorMsg != nil {
			errMsg = *task.ErrorMsg
		}
		result = fmt.Sprintf("失败: %s", errMsg)
	} else if task.Artifact != nil {
		result = *task.Artifact // 完整内容(report 接口要详细汇报)
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
