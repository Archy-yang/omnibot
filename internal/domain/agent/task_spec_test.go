package agent

import "testing"

// TestNewTaskSpec 仅 goal 的最小 TaskSpec,其余空。
func TestNewTaskSpec(t *testing.T) {
	s := NewTaskSpec("研究 Go")
	if s.Goal != "研究 Go" {
		t.Errorf("Goal = %q, want %q", s.Goal, "研究 Go")
	}
	if len(s.Deliverables) != 0 || len(s.CompletionCriteria) != 0 || len(s.Background) != 0 {
		t.Errorf("minimal TaskSpec 应无 deliverables/criteria/background")
	}
	if s.HasDetail() {
		t.Errorf("仅 goal 不应有 detail")
	}
}

// TestTaskSpec_HasDetail 含 deliverables/criteria/background 任一即有 detail。
func TestTaskSpec_HasDetail(t *testing.T) {
	if (TaskSpec{Goal: "g"}).HasDetail() {
		t.Error("只有 goal 不应有 detail")
	}
	if !(TaskSpec{Goal: "g", Deliverables: []Deliverable{{Name: "n"}}}).HasDetail() {
		t.Error("有 Deliverables 应有 detail")
	}
	if !(TaskSpec{Goal: "g", CompletionCriteria: []string{"c"}}).HasDetail() {
		t.Error("有 CompletionCriteria 应有 detail")
	}
	if !(TaskSpec{Goal: "g", Background: map[string]any{"k": "v"}}).HasDetail() {
		t.Error("有 Background 应有 detail")
	}
	if !(TaskSpec{Goal: "g", Constraints: &Constraints{MaxSteps: 5}}).HasDetail() {
		t.Error("有 Constraints 应有 detail")
	}
}

// TestNewAgentTask_Name 任务短名链路:spec.Name 落 AgentTask.Name(列表/详情展示用)。
// 编号 #N 只作引用锚点(反幻觉铁律依赖),人看的是短名;缺省留空由展示层回落 goal 摘要。
func TestNewAgentTask_Name(t *testing.T) {
	spec := NewTaskSpec("用 aihot.news 完整 feed 地址重新拉取 AIHOT 四个源的最新内容并汇总")
	spec.Name = "查AIHOT今日动态"
	task := NewAgentTask(42, spec, "web", "")
	if task.Name != "查AIHOT今日动态" {
		t.Errorf("Name = %q, want %q", task.Name, "查AIHOT今日动态")
	}

	// 缺省:Name 留空,不自行截断派生(展示层回落 goal)
	task2 := NewAgentTask(42, NewTaskSpec("查天气"), "web", "")
	if task2.Name != "" {
		t.Errorf("未起名时 Name 应为空, got %q", task2.Name)
	}
}
