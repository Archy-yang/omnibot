package tools

import (
	"strings"
	"testing"

	memorydomain "omnibot/internal/domain/memory"
)

// manage_memory_rules 工具测试(22号 R2):add 含存量删除回报/remove 未命中提示/list 格式。

type fakeRuleManager struct {
	rules      []*memorydomain.MemoryBlacklist
	added      string
	addDeleted int64
	addErr     error
	removedID  int64
	removed    bool
}

func (f *fakeRuleManager) ListBlacklist(int64) ([]*memorydomain.MemoryBlacklist, error) {
	return f.rules, nil
}
func (f *fakeRuleManager) AddBlacklist(_ int64, pattern string) (*memorydomain.MemoryBlacklist, int64, error) {
	if f.addErr != nil {
		return nil, 0, f.addErr
	}
	f.added = pattern
	return &memorydomain.MemoryBlacklist{ID: 9, UserID: 42, Pattern: pattern}, f.addDeleted, nil
}
func (f *fakeRuleManager) RemoveBlacklist(_ int64, id int64) (bool, error) {
	f.removedID = id
	return f.removed, nil
}

func TestMemoryRulesTool_Add(t *testing.T) {
	svc := &fakeRuleManager{addDeleted: 3}
	tool := CreateMemoryRulesTool(svc)

	out, err := tool.Execute(toolCtx(42), map[string]interface{}{"action": "add", "pattern": "银行卡号"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if svc.added != "银行卡号" {
		t.Errorf("pattern 应透传: %q", svc.added)
	}
	if !strings.Contains(out, "#9") || !strings.Contains(out, "3 条") {
		t.Errorf("add 文案应含规则 ID 与存量删除条数:\n%s", out)
	}

	// 零删除的文案分支
	svc2 := &fakeRuleManager{}
	out, err = CreateMemoryRulesTool(svc2).Execute(toolCtx(42), map[string]interface{}{"action": "add", "pattern": "x"})
	if err != nil || strings.Contains(out, "删除了") {
		t.Errorf("零删除不应出现删除文案: out=%q err=%v", out, err)
	}
}

func TestMemoryRulesTool_RemoveAndList(t *testing.T) {
	svc := &fakeRuleManager{removed: true}
	tool := CreateMemoryRulesTool(svc)

	out, err := tool.Execute(toolCtx(42), map[string]interface{}{"action": "remove", "rule_id": float64(5)})
	if err != nil || !strings.Contains(out, "已解除规则 #5") {
		t.Errorf("remove 成功文案异常: out=%q err=%v", out, err)
	}
	if svc.removedID != 5 {
		t.Errorf("rule_id 应透传: %d", svc.removedID)
	}

	// 未命中 → 可读错误
	svc2 := &fakeRuleManager{removed: false}
	if _, err := CreateMemoryRulesTool(svc2).Execute(toolCtx(42), map[string]interface{}{"action": "remove", "rule_id": float64(5)}); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Errorf("remove 未命中应提示先 list: %v", err)
	}

	// list
	svc3 := &fakeRuleManager{rules: []*memorydomain.MemoryBlacklist{{ID: 2, Pattern: "密码"}}}
	out, err = CreateMemoryRulesTool(svc3).Execute(toolCtx(42), map[string]interface{}{"action": "list"})
	if err != nil || !strings.Contains(out, "[2]") || !strings.Contains(out, "密码") {
		t.Errorf("list 输出应含规则 ID 与 pattern:\n%s err=%v", out, err)
	}

	// 参数缺失
	if _, err := tool.Execute(toolCtx(42), map[string]interface{}{"action": "add"}); err == nil {
		t.Error("add 缺 pattern 应报错")
	}
	if _, err := tool.Execute(toolCtx(42), map[string]interface{}{"action": "remove"}); err == nil {
		t.Error("remove 缺 rule_id 应报错")
	}
}
