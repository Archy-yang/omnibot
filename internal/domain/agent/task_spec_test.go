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
