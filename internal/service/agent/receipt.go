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
func BuildTaskReceipt(task *domainagent.AgentTask) string {
	// 去角色后 SubAgentType 是溯源标签(可空);空则显示"后台任务"。
	name := task.SubAgentType
	if name == "" {
		name = "后台任务"
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
	fmt.Fprintf(&b, "任务类型: %s\n", name)
	fmt.Fprintf(&b, "目标: %s\n", task.Goal)
	writeTaskContract(&b, task)
	b.WriteString("结果:\n")
	b.WriteString(result)
	return b.String()
}

// writeTaskContract 输出任务合同的详细信息(背景/交付物/完成标准)。
// 汇报锚定(§3.4 修订):主 Agent 只有看到"当初承诺交付什么、什么算完成",
// 才能在汇报时对照完成标准自查达标性(如实说"部分完成"),而非盲目转述子 Agent 结果。
// 无详细信息(只有 goal 的老合同)时输出空段落,不产生噪音。
func writeTaskContract(b *strings.Builder, task *domainagent.AgentTask) {
	spec := task.TaskSpec
	if len(spec.Background) > 0 {
		b.WriteString("背景:\n")
		for k, v := range spec.Background {
			fmt.Fprintf(b, "- %s: %v\n", k, v)
		}
	}
	if len(spec.Deliverables) > 0 {
		b.WriteString("交付物:\n")
		for _, d := range spec.Deliverables {
			if d.Description != "" {
				fmt.Fprintf(b, "- %s: %s\n", d.Name, d.Description)
			} else {
				fmt.Fprintf(b, "- %s\n", d.Name)
			}
		}
	}
	if len(spec.CompletionCriteria) > 0 {
		b.WriteString("完成标准:\n")
		for i, c := range spec.CompletionCriteria {
			fmt.Fprintf(b, "%d. %s\n", i+1, c)
		}
	}
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
//
//	主 Agent 需要详情再 query_task 取 artifact。
func BuildReportInstruction(tasks []*domainagent.AgentTask, fullArtifact bool) string {
	var b strings.Builder
	b.WriteString("以下是你之前安排的后台任务的完成回执,请向用户汇报这些任务的结果,")
	b.WriteString("用管家口吻转述(不要原样照搬回执格式,要自然语言汇报)。")
	b.WriteString("汇报前先对照每个任务的完成标准自查结果是否达标;")
	b.WriteString("未达标或部分完成的要如实说明(缺了什么、卡在哪),不要粉饰。")
	b.WriteString("汇报完即可,不需要用户再追问。\n\n")
	for _, task := range tasks {
		b.WriteString(buildTaskReceiptForReport(task, fullArtifact))
		b.WriteString("\n---\n")
	}
	return b.String()
}

// buildTaskReceiptForReport 按 fullArtifact 决定给全文或摘要。
func buildTaskReceiptForReport(task *domainagent.AgentTask, fullArtifact bool) string {
	if !fullArtifact {
		return BuildTaskReceipt(task) // 摘要(控制面)
	}
	// fullArtifact=true:给完整 artifact(report 接口详细汇报用)
	name := task.SubAgentType
	if name == "" {
		name = "后台任务"
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
	fmt.Fprintf(&b, "任务类型: %s\n", name)
	fmt.Fprintf(&b, "目标: %s\n", task.Goal)
	writeTaskContract(&b, task)
	b.WriteString("结果:\n")
	b.WriteString(result)
	return b.String()
}
