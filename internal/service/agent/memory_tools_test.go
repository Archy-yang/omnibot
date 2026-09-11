package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"
)

// 记忆检索工具测试(12-记忆系统技术方案 §8/M7 §10.6):
//   - search_memories 三段式:事项(M6.2) + 近期对话原文(M7) + 长期记忆;老子串路径兜底

func toolCtx(userID int64) context.Context {
	return context.WithValue(context.Background(), userIDContextKey, userID)
}

// fakeSearchableMemory 实现语义检索接口的假记忆服务。
type fakeSearchableMemory struct{}

func (fakeSearchableMemory) GetRecentForContext(_ context.Context, _ int64, limit int) ([]string, error) {
	return []string{"老子串记忆"}, nil
}

func (fakeSearchableMemory) SearchRecentMessages(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.MessageHit, error) {
	return nil, nil // 默认中期无命中
}

func (fakeSearchableMemory) SearchMemories(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.MemoryHit, error) {
	at := time.Date(2026, 9, 10, 8, 0, 0, 0, time.Local)
	return []memorydomain.MemoryHit{
		{Memory: &memorydomain.Memory{ID: 1, Content: "用户偏好简洁回复", Source: memorydomain.MemorySourceAuto, CreatedAt: at}, Score: 0.95},
		{Memory: &memorydomain.Memory{ID: 2, Content: "用户在上海工作", Source: memorydomain.MemorySourceManual, CreatedAt: at}, Score: 0.42},
	}, nil
}

// TestSearchMemoriesTool_Semantic 语义路径:返回来源标识(自动/手动)+ 记忆发生时间(PRD AC1.3 + 时间可观测)。
func TestSearchMemoriesTool_Semantic(t *testing.T) {
	tool := CreateSearchMemoriesTool(fakeSearchableMemory{})

	out, err := tool.Execute(toolCtx(42), map[string]interface{}{"query": "偏好"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"用户偏好简洁回复", "用户在上海工作", "自动", "2026-09-10"} {
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
