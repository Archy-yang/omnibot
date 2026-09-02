package agent

import (
	"context"
	"strings"
	"testing"

	memorydomain "omnibot/internal/domain/memory"
)

// 记忆检索工具测试(12-记忆系统技术方案 §8 / TDD#3/#11):
//   - search_memories:服务实现 MemorySearcher → 语义检索;仅实现旧 MemoryProvider → 老子串路径兜底
//   - search_history:DigestSearcher 检索纪要,返回纪要内容 + 溯源区间

func toolCtx(userID int64) context.Context {
	return context.WithValue(context.Background(), userIDContextKey, userID)
}

// fakeSearchableMemory 实现语义检索接口的假记忆服务。
type fakeSearchableMemory struct{}

func (fakeSearchableMemory) GetRecentForContext(_ context.Context, _ int64, limit int) ([]string, error) {
	return []string{"老子串记忆"}, nil
}

func (fakeSearchableMemory) SearchMemories(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.MemoryHit, error) {
	return []memorydomain.MemoryHit{
		{Memory: &memorydomain.Memory{ID: 1, Content: "用户偏好简洁回复", Source: memorydomain.MemorySourceAuto}, Score: 0.95},
		{Memory: &memorydomain.Memory{ID: 2, Content: "用户在上海工作", Source: memorydomain.MemorySourceManual}, Score: 0.42},
	}, nil
}

// TestSearchMemoriesTool_Semantic 语义路径:返回来源标识,自动/手动区分(PRD AC1.3)。
func TestSearchMemoriesTool_Semantic(t *testing.T) {
	tool := CreateSearchMemoriesTool(fakeSearchableMemory{})

	out, err := tool.Execute(toolCtx(42), map[string]interface{}{"query": "偏好"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"用户偏好简洁回复", "用户在上海工作", "自动"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺 %q:\n%s", want, out)
		}
	}
	// 手动记忆不该被标成自动
	if strings.Contains(out, "手动\n2. 自动") {
		t.Error("来源标识错乱")
	}
}

// fakeLegacyMemory 只实现旧接口的服务(如未来其他调用方),工具应走老子串路径不报错。
type fakeLegacyMemory struct{}

func (fakeLegacyMemory) GetRecentForContext(_ context.Context, _ int64, _ int) ([]string, error) {
	return []string{"老子串记忆-包含上海", "无关记忆"}, nil
}

func TestSearchMemoriesTool_LegacyFallback(t *testing.T) {
	tool := CreateSearchMemoriesTool(fakeLegacyMemory{})

	out, err := tool.Execute(toolCtx(42), map[string]interface{}{"query": "上海"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "老子串记忆-包含上海") || strings.Contains(out, "无关记忆") {
		t.Errorf("子串路径应只命中包含查询词的记忆:\n%s", out)
	}
}

// fakeDigestSearcher 假纪要检索服务。
type fakeDigestSearcher struct{}

func (fakeDigestSearcher) SearchDigests(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.DigestHit, error) {
	return []memorydomain.DigestHit{
		{Digest: &memorydomain.ConversationDigest{ID: 7, Summary: "聊了租房方案,倾向两居室", FromMessageID: 100, ToMessageID: 150}, Score: 0.9},
	}, nil
}

// TestSearchHistoryTool 落地实现:返回纪要 + 溯源区间,不再是"待实现"占位(TDD#11)。
func TestSearchHistoryTool(t *testing.T) {
	tool := CreateSearchHistoryTool(fakeDigestSearcher{})

	out, err := tool.Execute(toolCtx(42), map[string]interface{}{"query": "租房"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"聊了租房方案", "100", "150"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺 %q:\n%s", want, out)
		}
	}
}

// TestSearchHistoryTool_Empty 空结果给明确提示,Agent 能据此答"没找到"(PRD AC3.3)。
func TestSearchHistoryTool_Empty(t *testing.T) {
	tool := CreateSearchHistoryTool(emptyDigestSearcher{})

	out, err := tool.Execute(toolCtx(42), map[string]interface{}{"query": "不存在的话题"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "未找到") {
		t.Errorf("空结果应有明确未找到提示:\n%s", out)
	}
}

type emptyDigestSearcher struct{}

func (emptyDigestSearcher) SearchDigests(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.DigestHit, error) {
	return nil, nil
}
