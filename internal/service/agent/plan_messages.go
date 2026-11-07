package agent

// plan_messages.go — 计划异常/汇总文案(34 号)。
//
// 口径:
//   - 成功步骤**不推送**(用户反馈:9 步 9 条太打扰)
//   - 失败/暂停推一条,说明卡在第几步 + 原因 + 怎么继续
//   - 全部完成不在这里推:容器终态走普通任务汇报链路(Web /report、IM 转述)

import (
	"context"
	"fmt"
	"strings"

	domainagent "omnibot/internal/domain/agent"
)

// PlanChannelNotifier 计划异常消息的渠道发送(飞书 / 微信 ClawBot 各实现一份)。
type PlanChannelNotifier interface {
	// SendPlanText 发一条纯文本计划消息(暂停/异常)。
	SendPlanText(ctx context.Context, target, text string) error
}

// BuildPlanPauseText 某步失败 → 暂停文案(total 为计划总步数)。
func BuildPlanPauseText(plan *domainagent.AgentTask, child *domainagent.AgentTask, total int) string {
	if plan == nil || child == nil {
		return ""
	}
	reason := "执行失败"
	if child.ErrorMsg != nil && *child.ErrorMsg != "" {
		reason = *child.ErrorMsg
	}
	if total <= 0 {
		total = child.StepSeq
	}
	return fmt.Sprintf("⚠️ 计划「%s」第 %d/%d 步卡住了:%s\n原因:%s\n说「继续」我会重试这一步,说「取消」就停。",
		plan.Name, child.StepSeq, total, child.Name, reason)
}

// BuildPlanResultText 全部完成 → 容器 artifact(汇报素材)。
// 不调 LLM:确定、免费、必达;主 Agent 汇报时会转述成自然语言。
func BuildPlanResultText(plan *domainagent.AgentTask, children []*domainagent.AgentTask) string {
	if plan == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "计划「%s」共 %d 步,已全部完成。", plan.Name, len(children))
	for _, c := range children {
		fmt.Fprintf(&b, "\n%d. %s", c.StepSeq, c.Name)
		if c.Artifact != nil && *c.Artifact != "" {
			if line := domainagent.FirstLine(*c.Artifact, 80); line != "" {
				b.WriteString(":" + line)
			}
		}
	}
	if len(children) > 0 && children[len(children)-1].Artifact != nil && *children[len(children)-1].Artifact != "" {
		b.WriteString("\n\n最终产出:\n")
		b.WriteString(domainagent.TruncateDigest(*children[len(children)-1].Artifact))
	}
	return b.String()
}
