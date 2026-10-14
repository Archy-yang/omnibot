package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/internal/pkg/toolcore"
)

// 记忆检索工具测试(12-记忆系统技术方案 §8/M7 §10.6):
//   - search_memories 三段式:事项(M6.2) + 近期对话原文(M7) + 长期记忆;老子串路径兜底

func toolCtx(userID int64) context.Context {
	return context.WithValue(context.Background(), toolcore.UserIDKey, userID)
}

// fakeSearchableMemory 实现语义检索接口的假记忆服务。
type fakeSearchableMemory struct{}

func (fakeSearchableMemory) GetRecentForContext(_ context.Context, _ int64, limit int) ([]string, error) {
	return []string{"老子串记忆"}, nil
}

func (fakeSearchableMemory) SearchRecentMessages(_ context.Context, _ int64, _ string, _ int, _ *memorydomain.MemoryFilter) ([]memorydomain.MessageHit, error) {
	return nil, nil // 默认中期无命中
}

func (fakeSearchableMemory) SearchMemories(_ context.Context, _ int64, _ string, _ int, _ *memorydomain.MemoryFilter) ([]memorydomain.MemoryHit, error) {
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

// ===== 22 号 R1:search_memories 时间/主题参数 =====

// fakeFilteredMemory 记录收到的 filter,返回带 happened_at 的命中(时间可乱序,验证输出重排)。
type fakeFilteredMemory struct {
	gotFilter *memorydomain.MemoryFilter
}

func (f *fakeFilteredMemory) GetRecentForContext(_ context.Context, _ int64, _ int) ([]string, error) {
	return nil, nil
}
func (f *fakeFilteredMemory) SearchRecentMessages(_ context.Context, _ int64, _ string, _ int, _ *memorydomain.MemoryFilter) ([]memorydomain.MessageHit, error) {
	return nil, nil
}
func (f *fakeFilteredMemory) SearchMemories(_ context.Context, _ int64, _ string, _ int, filter *memorydomain.MemoryFilter) ([]memorydomain.MemoryHit, error) {
	f.gotFilter = filter
	late := time.Date(2026, 10, 8, 0, 0, 0, 0, time.Local)
	early := time.Date(2026, 9, 3, 0, 0, 0, 0, time.Local)
	return []memorydomain.MemoryHit{
		{Memory: &memorydomain.Memory{ID: 2, Content: "十月去了杭州", HappenedAt: &late, CreatedAt: late}, Score: 0.9},
		{Memory: &memorydomain.Memory{ID: 1, Content: "九月去了山里", HappenedAt: &early, CreatedAt: early}, Score: 0.8},
	}, nil
}

func (f *fakeFilteredMemory) MatterIDByTitle(_ int64, title string) (int64, bool) {
	if title == "旅行" {
		return 7, true
	}
	return 0, false
}

// T4 时间参数:透传 service + 时间线升序输出(happened_at 标注)。
func TestSearchMemoriesTool_TimeParams(t *testing.T) {
	svc := &fakeFilteredMemory{}
	tool := CreateSearchMemoriesTool(svc)

	out, err := tool.Execute(toolCtx(42), map[string]interface{}{
		"query": "旅行",
		"from":  "2026-09-01T00:00:00Z",
		"to":    "2026-10-31T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if svc.gotFilter == nil || svc.gotFilter.From == nil || svc.gotFilter.To == nil {
		t.Fatalf("from/to 应透传为 filter: %+v", svc.gotFilter)
	}
	// 时间线升序:九月在前
	iSep, iOct := strings.Index(out, "九月去了山里"), strings.Index(out, "十月去了杭州")
	if iSep < 0 || iOct < 0 || iSep > iOct {
		t.Errorf("时间参数下应按有效时间升序输出:\n%s", out)
	}
	if !strings.Contains(out, "2026-09-03") {
		t.Errorf("时间线应标注事件日期:\n%s", out)
	}
}

// T5 matter_title:命中 → filter.MatterID;未命中 → 可读提示不报错。
func TestSearchMemoriesTool_MatterTitle(t *testing.T) {
	svc := &fakeFilteredMemory{}
	tool := CreateSearchMemoriesTool(svc)

	_, err := tool.Execute(toolCtx(42), map[string]interface{}{"query": "行", "matter_title": "旅行"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if svc.gotFilter == nil || svc.gotFilter.MatterID == nil || *svc.gotFilter.MatterID != 7 {
		t.Fatalf("matter_title 应解析为 MatterID=7: %+v", svc.gotFilter)
	}

	_, err = tool.Execute(toolCtx(42), map[string]interface{}{"query": "行", "matter_title": "不存在的"})
	if err == nil || !strings.Contains(err.Error(), "未找到档案") {
		t.Errorf("未命中档案应给可读错误(引导 LLM 换查询): %v", err)
	}
}

// T6 非法时间格式 → 可读错误(带 RFC3339 提示)。
func TestSearchMemoriesTool_InvalidTime(t *testing.T) {
	tool := CreateSearchMemoriesTool(&fakeFilteredMemory{})
	_, err := tool.Execute(toolCtx(42), map[string]interface{}{"query": "x", "from": "上个月"})
	if err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Errorf("非法 from 应报 RFC3339 格式错: %v", err)
	}
}

// T7 无时间参数:行为同现状(相关度排序,不重排)。
func TestSearchMemoriesTool_NoTimeParams(t *testing.T) {
	svc := &fakeFilteredMemory{}
	tool := CreateSearchMemoriesTool(svc)
	out, err := tool.Execute(toolCtx(42), map[string]interface{}{"query": "旅行"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if svc.gotFilter != nil {
		t.Fatalf("无时间/主题参数 filter 应为 nil(现状行为): %+v", svc.gotFilter)
	}
	if strings.Index(out, "十月去了杭州") > strings.Index(out, "九月去了山里") {
		t.Errorf("无时间参数保持相关度排序(十月分高在前):\n%s", out)
	}
}
