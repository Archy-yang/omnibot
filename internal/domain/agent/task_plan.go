package agent

import (
	"fmt"
	"strings"
)

// task_plan.go — 计划编排的领域规则(34 号,v2.12 合并建模)。
//
// 建模:**计划就是一条 kind=plan 的任务,步骤是它的子任务**(ParentTaskID + StepSeq)。
// 不再有独立的 task_plans 表:任务中心只有一种实体,列表天然可以折叠。
//
// 边界(必须由服务层守护,领域只管规则):
//   - 容器不执行:没有 runner/超时/artifact 之外的执行语义,状态由推进器改写
//   - 子任务不单独汇报:创建即 reported=true,只有容器终态才汇报
//   - paused 只属于容器:普通任务不会进入该状态

const (
	// MinPlanSteps 两步起才叫计划:一步等于单任务。
	MinPlanSteps = 2
	// MaxPlanSteps 步骤上限:再多先合并,否则串行时长失控(9 × 180s ≈ 27 分钟)。
	MaxPlanSteps = 9
	// PlanDigestLimit 注入下一步的背景摘要上限(按 rune 计,避免中文切半)。
	PlanDigestLimit = 1200
	// PlanDigestTruncatedMark 截断标记:让下一步知道"信息不完整,可能需重查"。
	PlanDigestTruncatedMark = "…(已截断)"
)

// PlanStepSpec 计划的一步定义(派活时由主 Agent 给出,落库时变成子任务)。
type PlanStepSpec struct {
	Name               string
	Goal               string
	Deliverables       []Deliverable
	CompletionCriteria []string
}

// ValidatePlanSteps 校验步骤定义:2~9 步,name/goal 非空。
func ValidatePlanSteps(steps []PlanStepSpec) error {
	if len(steps) < MinPlanSteps {
		return fmt.Errorf("计划至少 %d 步(单步请直接派任务)", MinPlanSteps)
	}
	if len(steps) > MaxPlanSteps {
		return fmt.Errorf("计划最多 %d 步,超出请先合并步骤", MaxPlanSteps)
	}
	for i, s := range steps {
		if strings.TrimSpace(s.Goal) == "" {
			return fmt.Errorf("第 %d 步缺少 goal", i+1)
		}
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("第 %d 步缺少 name", i+1)
		}
	}
	return nil
}

// NewPlanTask 创建计划容器(pending,不执行;由推进器 Start 后置 running)。
func NewPlanTask(userID int64, spec TaskSpec, source, notifyTarget string, originTurnID int64) *AgentTask {
	t := NewAgentTask(userID, spec, source, notifyTarget)
	t.Kind = TaskKindPlan
	if originTurnID > 0 {
		t.OriginTurnID = &originTurnID
	}
	return t
}

// NewPlanChild 创建计划子任务(pending)。
// Reported 直接置 true:子任务永远不参与单任务汇报轮询,只有容器终态才汇报。
// 步骤的执行顺序与"第几步"信息放在 StepSeq 与 TaskSpec.Background。
func NewPlanChild(userID, parentID int64, seq int, spec TaskSpec, source, notifyTarget string) *AgentTask {
	t := NewAgentTask(userID, spec, source, notifyTarget)
	t.Kind = TaskKindTask
	pid := parentID
	t.ParentTaskID = &pid
	t.StepSeq = seq
	t.Reported = true
	return t
}

// PlanStepSpecFor 生成第 seq 步子任务的任务包:
// 注入计划名/总目标/进度,以及上一步产出摘要(信息不断链)。
func PlanStepSpecFor(plan *AgentTask, step PlanStepSpec, seq, total int, prevArtifact string) TaskSpec {
	bg := map[string]any{
		"计划":   plan.Name,
		"计划目标": plan.Goal,
		"进度":   fmt.Sprintf("第 %d/%d 步", seq, total),
	}
	if seq > 1 && prevArtifact != "" {
		bg["上一步结果"] = TruncateDigest(prevArtifact)
	}
	return TaskSpec{
		Goal:               step.Goal,
		Name:               step.Name,
		Deliverables:       step.Deliverables,
		CompletionCriteria: step.CompletionCriteria,
		Background:         bg,
	}
}

// PlanStepsText 计划的模板化总览(容器 artifact 用:给汇报提供素材)。
func PlanStepsText(children []*AgentTask) string {
	var b strings.Builder
	for _, c := range children {
		fmt.Fprintf(&b, "\n%d. %s(%s)", c.StepSeq, c.Name, c.Status)
		if c.Artifact != nil && *c.Artifact != "" {
			if line := FirstLine(*c.Artifact, 80); line != "" {
				b.WriteString(":" + line)
			}
		}
		if c.ErrorMsg != nil && *c.ErrorMsg != "" {
			b.WriteString(":失败原因 " + *c.ErrorMsg)
		}
	}
	return b.String()
}

// TruncateDigest 把产物截断成可注入下一步的摘要(rune 安全,超长带标记)。
func TruncateDigest(s string) string {
	runes := []rune(s)
	if len(runes) <= PlanDigestLimit {
		return s
	}
	return string(runes[:PlanDigestLimit]) + PlanDigestTruncatedMark
}

// FirstLine 取首行并按 rune 截断(空行跳过),用于摘要展示。
func FirstLine(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	line := s
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	runes := []rune(line)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return line
}
