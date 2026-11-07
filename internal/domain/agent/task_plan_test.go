package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 34 号(v2.12 合并建模):计划=容器任务,步骤=子任务。

func TestValidatePlanSteps(t *testing.T) {
	ok := func(n int) []PlanStepSpec {
		s := make([]PlanStepSpec, 0, n)
		for i := 0; i < n; i++ {
			s = append(s, PlanStepSpec{Name: "步", Goal: "目标"})
		}
		return s
	}
	cases := []struct {
		name    string
		steps   []PlanStepSpec
		wantErr bool
	}{
		{"空计划", nil, true},
		{"只有一步", ok(1), true},
		{"两步合法", ok(2), false},
		{"九步合法", ok(9), false},
		{"十步超限", ok(10), true},
		{"goal 为空", []PlanStepSpec{{Name: "步"}, {Name: "步", Goal: "g"}}, true},
		{"name 为空", []PlanStepSpec{{Goal: "g"}, {Name: "步", Goal: "g"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePlanSteps(c.steps)
			if c.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestNewPlanTaskAndChildren(t *testing.T) {
	spec := NewTaskSpec("做一份行程")
	spec.Name = "云南7日行"
	plan := NewPlanTask(42, spec, SourceWeb, "", 99)
	assert.Equal(t, TaskKindPlan, plan.Kind)
	assert.True(t, plan.IsPlan())
	assert.Equal(t, TaskStatusPending, plan.Status)
	require.NotNil(t, plan.OriginTurnID)

	child := NewPlanChild(42, 7, 2, NewTaskSpec("排路线"), SourceWeb, "")
	assert.Equal(t, TaskKindTask, child.Kind)
	assert.True(t, child.IsPlanChild())
	assert.Equal(t, 2, child.StepSeq)
	assert.True(t, child.Reported, "子任务创建即 reported,不进汇报轮询")
	require.NotNil(t, child.ParentTaskID)
	assert.Equal(t, int64(7), *child.ParentTaskID)
}

// 第二步必须拿到第一步结论(截断),第一步没有"上一步结果"。
func TestPlanStepSpecFor(t *testing.T) {
	plan := &AgentTask{ID: 1, Kind: TaskKindPlan, Name: "云南7日行", Goal: "做行程"}
	step := PlanStepSpec{Name: "排路线", Goal: "排 7 天路线"}

	first := PlanStepSpecFor(plan, step, 1, 3, "不应该出现")
	assert.Equal(t, "第 1/3 步", first.Background["进度"])
	assert.NotContains(t, first.Background, "上一步结果")

	long := strings.Repeat("云", PlanDigestLimit+10)
	second := PlanStepSpecFor(plan, step, 2, 3, long)
	prev, _ := second.Background["上一步结果"].(string)
	assert.True(t, strings.HasSuffix(prev, PlanDigestTruncatedMark))
	assert.Equal(t, "第 2/3 步", second.Background["进度"])
}

func TestPlanStepsText(t *testing.T) {
	a1 := "大理古城两晚"
	errMsg := "工具超时"
	children := []*AgentTask{
		{StepSeq: 1, Name: "查资料", Status: TaskStatusCompleted, Artifact: &a1},
		{StepSeq: 2, Name: "排路线", Status: TaskStatusFailed, ErrorMsg: &errMsg},
	}
	out := PlanStepsText(children)
	assert.Contains(t, out, "查资料")
	assert.Contains(t, out, "大理古城两晚")
	assert.Contains(t, out, "工具超时")
	assert.Contains(t, out, "排路线")
}

func TestFirstLine(t *testing.T) {
	assert.Equal(t, "第一行", FirstLine("第一行\n第二行", 20))
	out := FirstLine(strings.Repeat("云", 200), 50)
	assert.True(t, strings.HasSuffix(out, "…"))
	assert.LessOrEqual(t, len([]rune(out)), 51)
}
