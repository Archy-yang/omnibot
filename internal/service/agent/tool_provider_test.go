package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTestGlobalRegistry 构造与生产 Create* 工具打标对齐的小型全局工具池,便于纯函数单测。
func buildTestGlobalRegistry(t *testing.T) *ToolRegistry {
	t.Helper()
	r := NewToolRegistry()
	mk := func(name string, caps ...string) Tool {
		return Tool{
			Name:         name,
			Capabilities: caps,
			Execute:      func(ctx context.Context, args map[string]interface{}) (string, error) { return "", nil },
		}
	}
	for _, tool := range []Tool{
		mk("rss_reader", CapResearch, CapWeb, CapIngest),
		mk("web_read", CapResearch, CapWeb),
		mk("search_memories", CapMemory, CapResearch),
		mk("search_history", CapMemory, CapResearch),
		mk("get_current_time", CapBasic),
		mk("calculator", CapBasic),
		mk("request_input", CapInteractive),
	} {
		require.NoError(t, r.Register(tool))
	}
	return r
}

// TestResolveSubAgentTools_Basic 默认白名单 [research,interactive] → visible 精确复刻 researcher 卡工具集 + request_input。
func TestResolveSubAgentTools_Basic(t *testing.T) {
	got, err := ResolveSubAgentTools(buildTestGlobalRegistry(t), []string{CapResearch, CapInteractive})
	require.NoError(t, err)

	// knownNames = 全局池全部工具名(含被隐藏的 basic 类)
	assert.Equal(t, []string{
		"calculator", "get_current_time", "request_input", "rss_reader",
		"search_history", "search_memories", "web_read",
	}, got.KnownNames)

	// visible = 能力命中 + 强制基线
	assert.Equal(t, []string{
		"request_input", "rss_reader", "search_history", "search_memories", "web_read",
	}, got.Visible)
}

// TestResolveSubAgentTools_KnownVsHidden 存在但未命中能力的工具 → 出现在 known、不在 visible(隐藏非错)。
func TestResolveSubAgentTools_KnownVsHidden(t *testing.T) {
	got, err := ResolveSubAgentTools(buildTestGlobalRegistry(t), []string{CapBasic})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"calculator", "get_current_time", "request_input",
	}, got.Visible, "basic 命中 + request_input 基线")
	assert.Len(t, got.KnownNames, 7, "hidden 的 research/memory 类工具仍在全集")
}

// TestResolveSubAgentTools_TypoError 白名单含未知 capability → 报错(配错显性,仿 DSH knownNames 语义)。
func TestResolveSubAgentTools_TypoError(t *testing.T) {
	_, err := ResolveSubAgentTools(buildTestGlobalRegistry(t), []string{CapResearch, "banana"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "banana")
	assert.Contains(t, err.Error(), "unknown capability")
}

// TestResolveSubAgentTools_FrameworkCapabilityNotErrorWhenToolAbsent
// 默认白名单引用的 interactive(request_input 基线)即使当前池无工具承载也合法——不是配错。
// 回归:曾把校验误设为"当前注册工具所带能力并集",导致池无 request_input 时 interactive 被判为 unknown capability 报错。
func TestResolveSubAgentTools_FrameworkCapabilityNotErrorWhenToolAbsent(t *testing.T) {
	r := NewToolRegistry()
	r.Register(Tool{Name: "rss_reader", Capabilities: []string{CapResearch},
		Execute: func(context.Context, map[string]interface{}) (string, error) { return "", nil }})
	// 池无 request_input,但 default 白名单含 interactive:应成功且不报 unknown capability
	got, err := ResolveSubAgentTools(r, DefaultSubAgentCapabilities)
	require.NoError(t, err)
	assert.Equal(t, []string{"rss_reader"}, got.Visible)
}

// TestResolveSubAgentTools_AlwaysBaseline 白名单不含 interactive 时 request_input 仍可见(修复 #19 间隙)。
func TestResolveSubAgentTools_AlwaysBaseline(t *testing.T) {
	got, err := ResolveSubAgentTools(buildTestGlobalRegistry(t), []string{CapResearch})
	require.NoError(t, err)
	assert.Contains(t, got.Visible, "request_input", "request_input 恒可见,不依赖 interactive 白名单")
}

// TestResolveSubAgentTools_CapShift 组合能力裁剪:按能力而非角色枚举,改白名单即可重排可见集。
func TestResolveSubAgentTools_CapShift(t *testing.T) {
	got, err := ResolveSubAgentTools(buildTestGlobalRegistry(t), []string{CapWeb})
	require.NoError(t, err)
	// CapWeb 覆盖 web_read + rss_reader(cap 含 web),+ request_input 基线
	assert.Equal(t, []string{
		"request_input", "rss_reader", "web_read",
	}, got.Visible)
}
