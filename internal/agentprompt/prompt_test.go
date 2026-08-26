package agentprompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------- PromptRegistry 核心单测(11-Prompt管理技术方案 §8) ----------------

// TestAssemble_StaticOrdering 两条静态 section 按 Order 升序拼接。
func TestAssemble_StaticOrdering(t *testing.T) {
	r := NewPromptRegistry()
	require.NoError(t, r.Register(StaticSection("host", "", -100, "你是全平台智能助手")))
	require.NoError(t, r.Register(StaticSection("a", "", 10, "A")))
	require.NoError(t, r.Register(StaticSection("b", "", 5, "B")))
	require.NoError(t, r.Register(StaticSection("c", "", 1, "C")))

	out, err := r.Assemble(PromptCtx{})
	require.NoError(t, err)
	assert.Equal(t, "你是全平台智能助手CBA", out, "身份段在-100最前,其余按Order升序:C(1) B(5) A(10)")
}

// TestAssemble_Scoping 全局 section 恒参与;scope section 仅命中 c.Scopes 才参与。
func TestAssemble_Scoping(t *testing.T) {
	r := NewPromptRegistry()
	require.NoError(t, r.Register(StaticSection("global", "", 0, "G")))
	require.NoError(t, r.Register(StaticSection("p", ScopeMain, 100, "M")))

	// 命中 main:全局 + main 都参与
	out, err := r.Assemble(PromptCtx{Scopes: []ScopeKey{ScopeMain}})
	require.NoError(t, err)
	require.Equal(t, "GM", out)

	// 不传 scope:全局参与,main 作用域节不参与
	out, err = r.Assemble(PromptCtx{})
	require.NoError(t, err)
	require.Equal(t, "G", out)

	// 命中其它 scope:main 节仍不参与
	out, err = r.Assemble(PromptCtx{Scopes: []ScopeKey{"sub:researcher"}})
	require.NoError(t, err)
	require.Equal(t, "G", out)
}

// TestAssemble_CompleteEscapeHatch complete section 独占整个 prompt;两条 complete 抛错。
func TestAssemble_CompleteEscapeHatch(t *testing.T) {
	r := NewPromptRegistry()
	require.NoError(t, r.Register(StaticSection("a", "", 0, "A")))
	s := StaticSection("full", "", 0, "FULL:{{who}}")
	s.Complete = true
	require.NoError(t, r.Register(s))

	// complete 独占,忽略其它 section;仍解析其内 {{var}}
	require.NoError(t, r.RegisterVariable("who", func(PromptCtx) (string, error) { return "world", nil }))
	out, err := r.Assemble(PromptCtx{})
	require.NoError(t, err)
	require.Equal(t, "FULL:world", out)

	// 两条 complete -> 抛错
	r2 := NewPromptRegistry()
	require.NoError(t, r2.Register(StaticSection("c1", "", 0, "ONE")))
	require.NoError(t, r2.Register(StaticSection("c2", "", 0, "TWO")))
	// 手动把两条都标 complete 再组装(构造用测试辅助)
	r2.sections[0].Complete = true
	r2.sections[1].Complete = true
	_, err = r2.Assemble(PromptCtx{})
	require.Error(t, err, "多于一条 complete section 必须报错")
}

// TestAssemble_VariableInterpolation {{name}} 被解析;缺失变量抛错。
func TestAssemble_VariableInterpolation(t *testing.T) {
	r := NewPromptRegistry()
	require.NoError(t, r.RegisterVariable("goal", func(PromptCtx) (string, error) { return "查高铁票", nil }))
	require.NoError(t, r.Register(StaticSection("g", "", 0, "目标:{{goal}},请处理")))

	out, err := r.Assemble(PromptCtx{})
	require.NoError(t, err)
	require.Equal(t, "目标:查高铁票,请处理", out)

	// 缺失变量 -> 抛错
	r2 := NewPromptRegistry()
	require.NoError(t, r2.Register(StaticSection("g2", "", 0, "{{nope}}")))
	_, err = r2.Assemble(PromptCtx{})
	require.Error(t, err, "引用未注册变量必须报错")
}

// TestRegisterVariable_ValidatesName 变量名须匹配 [a-z][a-z0-9_]*。
func TestRegisterVariable_ValidatesName(t *testing.T) {
	r := NewPromptRegistry()
	require.NoError(t, r.RegisterVariable("goal", func(PromptCtx) (string, error) { return "x", nil }))
	require.Error(t, r.RegisterVariable("BadName", func(PromptCtx) (string, error) { return "x", nil }), "大写开头非法")
	require.Error(t, r.RegisterVariable("two words", func(PromptCtx) (string, error) { return "x", nil }), "含空格非法")
}

// TestRegister_Duplicate 同 (Scope,Name) 重复注册抛错;不同 scope 同名合法。
func TestRegister_Duplicate(t *testing.T) {
	r := NewPromptRegistry()
	require.NoError(t, r.Register(StaticSection("x", "", 0, "X1")))
	err := r.Register(StaticSection("x", "", 0, "X2"))
	require.Error(t, err, "同 scope 同名必须抛错")

	// 相同 name、不同 scope —— 合法
	require.NoError(t, r.Register(StaticSection("x", ScopeMain, 0, "MX")))
	require.NoError(t, r.Register(StaticSection("x", ScopeKey("sub:r"), 0, "RX")))
}

// TestProvider_DynamicSection provider 按 c.Request 求值;Text/Provider 双空不产文本。
func TestProvider_DynamicSection(t *testing.T) {
	r := NewPromptRegistry()
	require.NoError(t, r.Register(PromptSection{
		Name:  "dyn",
		Order: 0,
		Provider: func(c PromptCtx) string {
			return "user=" + c.Request["user"]
		},
	}))
	out, err := r.Assemble(PromptCtx{Request: map[string]string{"user": "阿明"}})
	require.NoError(t, err)
	require.Equal(t, "user=阿明", out)

	// Provider 优先于 Text
	r2 := NewPromptRegistry()
	require.NoError(t, r2.Register(PromptSection{
		Name: "both", Order: 0, Text: "static",
		Provider: func(c PromptCtx) string { return "dynamic" },
	}))
	out, err = r2.Assemble(PromptCtx{})
	require.NoError(t, err)
	require.Equal(t, "dynamic", out, "Provider 优先于 Text")

	// 双空不产文本
	r3 := NewPromptRegistry()
	require.NoError(t, r3.Register(StaticSection("a", "", 0, "A")))
	require.NoError(t, r3.Register(PromptSection{Name: "empty", Order: 5}))
	out, err = r3.Assemble(PromptCtx{})
	require.NoError(t, err)
	require.Equal(t, "A", out, "Text/Provider 双空的 section 跳过")
}

// TestHas 查询某 section 是否已注册(按与 scope 组合)。
func TestHas(t *testing.T) {
	r := NewPromptRegistry()
	require.NoError(t, r.Register(StaticSection("delegation_rules", ScopeMain, 100, "must delegate")))
	require.NoError(t, r.Register(StaticSection("harness_identity", "", -100, "harness")))

	assert.True(t, r.Has(ScopeMain, "delegation_rules"))
	assert.False(t, r.Has(ScopeKey("sub:researcher"), "delegation_rules"), "作用域不命中则视为未装配")
	assert.False(t, r.Has(ScopeMain, "reporting_rules"), "未注册返回 false")
	assert.True(t, r.Has("", "harness_identity"), "全局 section 可用空 scope 查到")
}
