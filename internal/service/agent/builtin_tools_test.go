package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMemoryProvider for testing search_memories
type mockMemoryProvider struct {
	memories []string
	err      error
}

func (m *mockMemoryProvider) GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.memories, nil
}

func TestBuiltinTools_GetCurrentTime(t *testing.T) {
	tool := CreateGetCurrentTimeTool()
	result, err := tool.Execute(context.Background(), nil)
	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestBuiltinTools_Calculator(t *testing.T) {
	tool := CreateCalculatorTool()

	result, err := tool.Execute(context.Background(), map[string]interface{}{"expression": "2 + 3"})
	require.NoError(t, err)
	assert.Equal(t, "5", result)

	result, err = tool.Execute(context.Background(), map[string]interface{}{"expression": "10 * 5"})
	require.NoError(t, err)
	assert.Equal(t, "50", result)

	result, err = tool.Execute(context.Background(), map[string]interface{}{"expression": "(2+3)*4"})
	require.NoError(t, err)
	assert.Equal(t, "20", result)

	_, err = tool.Execute(context.Background(), map[string]interface{}{"expression": "invalid"})
	assert.Error(t, err)
}

func TestBuiltinTools_Calculator_Security(t *testing.T) {
	tool := CreateCalculatorTool()

	_, err := tool.Execute(context.Background(), map[string]interface{}{"expression": "os.system('ls')"})
	assert.Error(t, err)
}

func TestBuiltinTools_SearchMemories(t *testing.T) {
	mockSvc := &mockMemoryProvider{memories: []string{"我喜欢简洁的回答", "我的生日是1月1日"}}
	tool := CreateSearchMemoriesTool(mockSvc)

	result, err := tool.Execute(context.Background(), map[string]interface{}{"query": "生日"})
	require.NoError(t, err)
	assert.Contains(t, result, "1月1日")
	assert.NotContains(t, result, "简洁")
}

func TestBuiltinTools_SearchMemories_NoMatch(t *testing.T) {
	mockSvc := &mockMemoryProvider{memories: []string{"我喜欢简洁的回答"}}
	tool := CreateSearchMemoriesTool(mockSvc)

	result, err := tool.Execute(context.Background(), map[string]interface{}{"query": "天气"})
	require.NoError(t, err)
	assert.Contains(t, result, "未找到")
}

// ===== M6.2 search_memories 两段式(事项优先) =====

// matterFirstFake 同时实现 MemorySearcher + MatterSearcher。
type matterFirstFake struct {
	matters []memorydomain.MatterHit
	hits    []memorydomain.MemoryHit
	recent  []memorydomain.MessageHit // M7 中期区
}

func (f *matterFirstFake) SearchMatters(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.MatterHit, error) {
	return f.matters, nil
}

func (f *matterFirstFake) SearchRecentMessages(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.MessageHit, error) {
	return f.recent, nil
}

func (f *matterFirstFake) SearchMemories(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.MemoryHit, error) {
	return f.hits, nil
}

func (f *matterFirstFake) GetRecentForContext(_ context.Context, _ int64, _ int) ([]string, error) {
	return nil, nil
}

func TestSearchMemoriesTool_MatterFirstRendering(t *testing.T) {
	mid := int64(7)
	at := time.Date(2026, 9, 9, 10, 0, 0, 0, time.Local)
	matter := &memorydomain.Matter{ID: mid, Title: "十一旅行", StateDesc: "机票别墅已订,交通未定", UpdatedAt: at}
	fact := &memorydomain.Memory{ID: 101, Content: "用户注重性价比", Kind: "fact", MatterID: &mid, CreatedAt: at}
	loop := &memorydomain.Memory{ID: 102, Content: "待核实实时票价", Kind: "loop", MatterID: &mid, CreatedAt: at}
	other := &memorydomain.Memory{ID: 103, Content: "用户是后端工程师", Source: memorydomain.MemorySourceAuto, CreatedAt: at}

	fake := &matterFirstFake{
		matters: []memorydomain.MatterHit{{Matter: matter, Facts: []*memorydomain.Memory{fact, loop}, Score: 0.5}},
		hits: []memorydomain.MemoryHit{
			{Memory: loop, Score: 0.9},  // 已随事项全景展示过 → 应去重
			{Memory: other, Score: 0.4}, // 未展示 → 出现在散点区
		},
	}
	tool := CreateSearchMemoriesTool(fake)

	out, err := tool.Execute(context.Background(), map[string]interface{}{"query": "旅行怎么样了"})
	require.NoError(t, err)
	require.Contains(t, out, "【事项】十一旅行")
	require.Contains(t, out, "当前状态:机票别墅已订,交通未定")
	require.Contains(t, out, "更新于 2026-09-09")
	require.Contains(t, out, "用户注重性价比(fact · 2026-09-09)")
	require.Contains(t, out, "待核实实时票价(loop · 2026-09-09)")
	require.Contains(t, out, "用户是后端工程师(自动记忆 · 2026-09-09)")
	require.Equal(t, 1, strings.Count(out, "待核实实时票价"), "事项全景里已展示的记忆不应在散点区重复")
}

// TestSearchMemoriesTool_ThreeSections M7 三段式:事项区 → 近期对话原文区(带时间/角色/消息号) → 长期区。
func TestSearchMemoriesTool_ThreeSections(t *testing.T) {
	mid := int64(7)
	matter := &memorydomain.Matter{ID: mid, Title: "充电台账", StateDesc: "已读完 23 条记录", UpdatedAt: time.Now()}
	fact := &memorydomain.Memory{ID: 101, Content: "用户车辆电池 78 度", Kind: "fact", MatterID: &mid, CreatedAt: time.Now()}
	fake := &matterFirstFake{
		matters: []memorydomain.MatterHit{{Matter: matter, Facts: []*memorydomain.Memory{fact}, Score: 0.5}},
		recent: []memorydomain.MessageHit{
			{MessageID: 61, Role: "user", Content: "你能看到我充电记录的多维表格吗", CreatedAt: time.Date(2026, 9, 9, 23, 43, 0, 0, time.Local), Score: 0.9},
		},
		hits: []memorydomain.MemoryHit{
			{Memory: &memorydomain.Memory{ID: 103, Content: "用户注重性价比", Source: memorydomain.MemorySourceAuto, CreatedAt: time.Now()}, Score: 0.4},
		},
	}
	tool := CreateSearchMemoriesTool(fake)

	out, err := tool.Execute(context.Background(), map[string]interface{}{"query": "充电"})
	require.NoError(t, err)
	require.Contains(t, out, "【事项】充电台账")
	require.Contains(t, out, "【近期对话】1 段相关原文:")
	require.Contains(t, out, "[2026-09-09 23:43 user] [#61] 你能看到我充电记录的多维表格吗")
	require.Contains(t, out, "1. 用户注重性价比(自动记忆")
	// 顺序:事项 → 近期 → 长期
	require.Less(t, strings.Index(out, "【事项】"), strings.Index(out, "【近期对话】"))
	require.Less(t, strings.Index(out, "【近期对话】"), strings.Index(out, "1. 用户注重性价比"))
}

// TestSearchMemoriesTool_RecentEmpty_SkipsSection 中期无命中时该段整段省略(不出现空标题)。
func TestSearchMemoriesTool_RecentEmpty_SkipsSection(t *testing.T) {
	fake := &matterFirstFake{
		hits: []memorydomain.MemoryHit{
			{Memory: &memorydomain.Memory{ID: 103, Content: "用户是后端工程师", CreatedAt: time.Now()}, Score: 0.4},
		},
	}
	tool := CreateSearchMemoriesTool(fake)
	out, err := tool.Execute(context.Background(), map[string]interface{}{"query": "工程师"})
	require.NoError(t, err)
	require.NotContains(t, out, "【近期对话】")
	require.Contains(t, out, "用户是后端工程师")
}

func TestSearchMemoriesTool_NoMatterHit_FallsBackToMemories(t *testing.T) {
	other := &memorydomain.Memory{ID: 103, Content: "用户是后端工程师", Source: memorydomain.MemorySourceAuto}
	fake := &matterFirstFake{
		hits: []memorydomain.MemoryHit{{Memory: other, Score: 0.4}},
	}
	tool := CreateSearchMemoriesTool(fake)

	out, err := tool.Execute(context.Background(), map[string]interface{}{"query": "工程师"})
	require.NoError(t, err)
	require.NotContains(t, out, "【事项】")
	require.Contains(t, out, "1. 用户是后端工程师(自动记忆)")
}
