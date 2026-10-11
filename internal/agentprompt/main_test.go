package agentprompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 主 Agent prompt section 化的 golden 迁移测试(11-Prompt管理 §8)。
// 拆成 registry section 后,Assemble 输出必须与同一常量块顺序拼接逐字节一致(防组装回归)。

// TestMainAgentMigration_Golden_WithSubAgents hasSubAgents=true:组装输出 == 同一常量块顺序拼接。
// 锁定 registry 的排序/拼接/去副作用对同一来源常量吐出与朴素拼接一致的结果(防组装回归)。
func TestMainAgentMigration_Golden_WithSubAgents(t *testing.T) {
	got, err := BuildMainAgentSystemPrompt(true)
	require.NoError(t, err)
	want := DefaultSystemPrompt + MainResponseStylePrompt + MainDelegationRulesPrompt + MainReportingRulesPrompt + MainTaskMgmtToolsPrompt + MainSubscriptionRulesPrompt
	require.Equal(t, want, got,
		"主 Agent(有子 Agent)的 registry 组装必须与默认拼接逐字节一致")
}

// TestMainAgentMigration_Golden_NoSubAgents hasSubAgents=false:组装输出 == base + 表达方式。
func TestMainAgentMigration_Golden_NoSubAgents(t *testing.T) {
	got, err := BuildMainAgentSystemPrompt(false)
	require.NoError(t, err)
	require.Equal(t, DefaultSystemPrompt+MainResponseStylePrompt, got,
		"无子 Agent 时只有基础人格 + 表达方式")
}

// TestMainAgentSections_Scoping 派活/汇报/任务管理仅 hasSubAgents 时存在;
// 基础人格与表达方式(persona 层)恒在。
func TestMainAgentSections_Scoping(t *testing.T) {
	withSub := MainAgentSections(true)
	assert.True(t, sectionHas(withSub, ScopeMain, "delegation_rules"))
	assert.True(t, sectionHas(withSub, ScopeMain, "reporting_rules"))
	assert.True(t, sectionHas(withSub, ScopeMain, "task_mgmt"))
	assert.True(t, sectionHas(withSub, ScopeMain, "subscription_rules"))
	assert.True(t, sectionHas(withSub, ScopeMain, "response_style"))

	noSub := MainAgentSections(false)
	assert.False(t, sectionHas(noSub, ScopeMain, "delegation_rules"), "无子 Agent 时不装配派活 section")
	assert.False(t, sectionHas(noSub, ScopeMain, "reporting_rules"))
	assert.False(t, sectionHas(noSub, ScopeMain, "task_mgmt"))
	assert.False(t, sectionHas(noSub, ScopeMain, "subscription_rules"))
	assert.True(t, sectionHas(noSub, ScopeMain, "agent_base"), "基础人格恒在")
	assert.True(t, sectionHas(noSub, ScopeMain, "response_style"), "表达方式是 persona 层,恒在")
}

// TestMainAgentSections_Order 主 Agent sections 按 order 排序:base(-100) 在 delegation(100) 前。
func TestMainAgentSections_Order(t *testing.T) {
	sections := MainAgentSections(true)
	for i := 1; i < len(sections); i++ {
		assert.LessOrEqual(t, sections[i-1].Order, sections[i].Order,
			"sections 须按 Order 升序:base(-100) < delegation(100) < reporting(110) < task_mgmt(120)")
	}
}

func sectionHas(sections []PromptSection, scope ScopeKey, name string) bool {
	for _, s := range sections {
		if s.Scope == scope && s.Name == name {
			return true
		}
	}
	return false
}

// TestMainDelegationRulesPrompt_AntiHallucination 反幻觉铁律守护(2026-09-14 事故):
// 未调 delegate 却说"已安排(任务 #N)"的编造编号事故——铁律关键句不得在后续编辑中丢失。
func TestMainDelegationRulesPrompt_AntiHallucination(t *testing.T) {
	for _, want := range []string{
		"铁律",
		"只有当你本轮真实调用了 delegate 工具",
		"严禁编造",
		"推算",
		"沿用历史编号",
		"当场穿帮",
		// 判断原则(2026-10-07 重构:由场景枚举改为「能力+量级」两问,枚举追不上能力演进——
		// 先是游玩规划被直调,后是计算请求被答"没有python环境",都是枚举漏项)
		// 2026-10-07 二轮(prompt-派活规则评审):两问正交化 + 删掉工具/能力枚举 + 无能宣言升格铁律
		"先问能力,再问量级",
		"直接看,不要凭印象",
		"你没有的执行能力",
		"不在此处列举",
		"不许替后台宣布无能",
		"多步检索跨来源汇总",
		"即使手上连接器工具能查",
		"直调的样子",
		"派活的样子",
		"查个天气",
		"拿不准时回到上面两问重新判断",
		// 派活自检 + 例子(2026-09-25 二次漏派:餐饮追问被 10 连发 mcp_call 直调)
		"派活自检",
		"追问不豁免",
		"并发一次发多个查询也算",
		"对照感受量级",
	} {
		assert.Contains(t, MainDelegationRulesPrompt, want, "派活规则缺少关键句 %q", want)
	}
	// 反枚举守护(2026-10-07 prompt-派活规则评审 P0-1):派活规则中不得出现"工具名/能力清单"——
	// 清单必然与代码脱节(已发生:原"只覆盖 mcp_call/calculator"漏了 weather,
	// 而同段第一个例子恰好依赖 weather 直调)。新增能力应改 tool description 或 tools 注册,
	// 不得回来补 prompt 枚举。
	for _, banned := range []string{"calculator 算式", "mcp_call 查连接器", "沙箱执行 Python/Shell"} {
		assert.NotContains(t, MainDelegationRulesPrompt, banned,
			"派活规则出现了能力枚举 %q——能力清单会随实现脱节,请改为「看 tools 参数」", banned)
	}
	// 铁律必须是规则体的第一段(优先级最高,不允许被其他段落稀释),
	// 且必须同时覆盖"编造 task_id"与"虚假无能声明"两面(2026-10-07 二轮)。
	first := strings.Index(MainDelegationRulesPrompt, "【铁律")
	second := strings.Index(MainDelegationRulesPrompt, "【判断原则")
	require.NotEqual(t, -1, first)
	require.NotEqual(t, -1, second)
	assert.Less(t, first, second, "铁律段必须位于「判断原则」之前")
	assert.Contains(t, MainDelegationRulesPrompt[first:second], "一、说了就得做过", "铁律须含第一条")
	assert.Contains(t, MainDelegationRulesPrompt[first:second], "二、做不了就得有证据", "铁律须含第二条")
}

// TestMainResponseStylePrompt_ButlerVoice 表达方式守护:管家口吻、不叙述工具调用过程
// (task#161 用户反馈:"用高德 MCP 实查了……"这类技术过程叙述不该出现在回复里)。
func TestMainResponseStylePrompt_ButlerVoice(t *testing.T) {
	for _, want := range []string{
		"私人管家",
		"直接给结果",
		"不提工具名",
		"MCP",
		"技术名词",
		"如实",
		// 护栏:不得弱化行为铁律——派活确认这类基于真实工具调用的口语保留
		"已安排",
	} {
		assert.Contains(t, MainResponseStylePrompt, want, "表达方式缺少关键句 %q", want)
	}
}

// TestMainDelegationRulesPrompt_ConciseHumanReply 派活后的回复必须是一句口语人话
// (task#20 反馈:模型把给子 Agent 的合同复述成三大节标题排版文,机械化没人味)。
func TestMainDelegationRulesPrompt_ConciseHumanReply(t *testing.T) {
	for _, want := range []string{
		"一句话",
		"不要复述",
		"短名",
	} {
		assert.Contains(t, MainDelegationRulesPrompt, want, "派活规则缺少关键句 %q", want)
	}
}
