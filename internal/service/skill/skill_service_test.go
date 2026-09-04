package skill

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skilldomain "omnibot/internal/domain/skill"
	agentpkg "omnibot/internal/service/agent"
)

// ---- mock repo ----

type mockSkillRepository struct {
	upserted []skilldomain.BuiltinDef
	upsertErr error
	rows     []*skilldomain.Skill
	listErr  error
	enabledName   string
	enabledValue  bool
	setEnabledErr error
	upsertedMCP  []skilldomain.MCPToolDef
	deletedNotIn []string
	deletedServers []string
}

func (m *mockSkillRepository) UpsertBuiltin(def skilldomain.BuiltinDef) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	m.upserted = append(m.upserted, def)
	return nil
}

func (m *mockSkillRepository) List() ([]*skilldomain.Skill, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.rows, nil
}

func (m *mockSkillRepository) GetByName(name string) (*skilldomain.Skill, error) {
	for _, r := range m.rows {
		if r.Name == name {
			return r, nil
		}
	}
	return nil, nil
}

func (m *mockSkillRepository) SetEnabled(name string, enabled bool) error {
	if m.setEnabledErr != nil {
		return m.setEnabledErr
	}
	m.enabledName = name
	m.enabledValue = enabled
	for _, r := range m.rows {
		if r.Name == name {
			r.Enabled = enabled
		}
	}
	return nil
}

func (m *mockSkillRepository) UpsertMCPTool(def skilldomain.MCPToolDef) error {
	m.upsertedMCP = append(m.upsertedMCP, def)
	// 镜像真实 repo 的 upsert 语义:按 Name 更新定义字段,**保留既有 Enabled**
	for _, r := range m.rows {
		if r.Name == def.Name {
			r.DisplayName = def.DisplayName
			r.Description = def.Description
			r.ParamsSchema = def.ParamsSchema
			r.MainVisible = def.MainVisible
			r.MCPServer = def.MCPServer
			return nil
		}
	}
	m.rows = append(m.rows, &skilldomain.Skill{
		Name: def.Name, DisplayName: def.DisplayName, Description: def.Description,
		Source: skilldomain.SourceMCP, Enabled: def.Enabled, MainVisible: def.MainVisible,
		MCPServer: def.MCPServer, ParamsSchema: def.ParamsSchema,
	})
	return nil
}

func (m *mockSkillRepository) DeleteMCPSkillsNotIn(serverNames []string) (int64, error) {
	m.deletedNotIn = serverNames
	return 0, nil
}

func (m *mockSkillRepository) DeleteMCPSkillsByServer(serverName string) (int64, error) {
	m.deletedServers = append(m.deletedServers, serverName)
	kept := m.rows[:0]
	for _, r := range m.rows {
		if !(r.Source == skilldomain.SourceMCP && r.MCPServer == serverName) {
			kept = append(kept, r)
		}
	}
	m.rows = kept
	return 1, nil
}

// ---- 工具 ----

func timeBuilder() agentpkg.Tool { return agentpkg.CreateGetCurrentTimeTool() }
func calcBuilder() agentpkg.Tool { return agentpkg.CreateCalculatorTool() }

func newService(repo *mockSkillRepository) *SkillService {
	svc := NewSkillService(repo)
	svc.RegisterBuiltin(timeBuilder)
	svc.RegisterBuiltin(calcBuilder)
	return svc
}

func newRegistries() (main, global *agentpkg.ToolRegistry) {
	main = agentpkg.NewToolRegistry()
	global = agentpkg.NewToolRegistry()
	return
}

func hasTool(r *agentpkg.ToolRegistry, name string) bool {
	_, ok := r.Get(name)
	return ok
}

func skillRow(name string, enabled bool) *skilldomain.Skill {
	return &skilldomain.Skill{
		Name:        name,
		DisplayName: name,
		Description: "desc",
		Source:      skilldomain.SourceBuiltin,
		Enabled:     enabled,
		MainVisible: true,
	}
}

// ---- SeedBuiltins ----

// 测试 1:seed 把代码内定义 upsert 进 repo,定义字段与工厂函数一致(单一事实源)。
func TestSeedBuiltins_UpsertsDefinitions(t *testing.T) {
	repo := &mockSkillRepository{}
	svc := newService(repo)

	err := svc.SeedBuiltins()
	require.NoError(t, err)
	require.Len(t, repo.upserted, 2)

	byName := map[string]skilldomain.BuiltinDef{}
	for _, d := range repo.upserted {
		byName[d.Name] = d
	}
	timeDef, ok := byName["get_current_time"]
	require.True(t, ok)
	assert.Equal(t, "获取当前的日期和时间", timeDef.Description)
	assert.Equal(t, []string{agentpkg.CapBasic}, timeDef.Capabilities)
	assert.Equal(t, "查询了当前时间", timeDef.DisplayName)

	calcDef, ok := byName["calculator"]
	require.True(t, ok)
	assert.Contains(t, calcDef.Parameters, "properties")
}

// 测试 2:enabled 技能且执行体存在 → 进 main+global 两池,能力标签原样还原。
func TestApplyTo_EnabledSkillInBothRegistries(t *testing.T) {
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{skillRow("get_current_time", true)}}
	svc := newService(repo)
	main, global := newRegistries()

	err := svc.ApplyTo(main, global)
	require.NoError(t, err)
	assert.True(t, hasTool(main, "get_current_time"))
	assert.True(t, hasTool(global, "get_current_time"))

	tool, ok := global.Get("get_current_time")
	require.True(t, ok)
	assert.Equal(t, []string{agentpkg.CapBasic}, tool.Capabilities)
	assert.Equal(t, "获取当前的日期和时间", tool.Description)
}

// 测试 3:停用的技能不进任何池。
func TestApplyTo_DisabledSkipped(t *testing.T) {
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{skillRow("calculator", false)}}
	svc := newService(repo)
	main, global := newRegistries()

	err := svc.ApplyTo(main, global)
	require.NoError(t, err)
	assert.False(t, hasTool(main, "calculator"))
	assert.False(t, hasTool(global, "calculator"))
}

// 测试 3.5:子 Agent 专属技能(MainVisible=false,如抓取类 rss/web_read)只进 global 池,
// 不进主 Agent 池(方向 B:主 Agent 是管家,联网抓取必须 delegate 派活)。
func TestApplyTo_SubOnlySkill_NotInMain(t *testing.T) {
	row := skillRow("rss_reader", true)
	row.MainVisible = false
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{row}}
	svc := newService(repo)
	svc.RegisterBuiltin(func() agentpkg.Tool { return agentpkg.CreateRSSReaderTool() })
	main, global := newRegistries()

	err := svc.ApplyTo(main, global)
	require.NoError(t, err)
	assert.True(t, hasTool(global, "rss_reader"))
	assert.False(t, hasTool(main, "rss_reader"))
}

// 测试 4:执行体缺失的技能(builder 未注册)隐藏,不报错。
func TestApplyTo_MissingBuilderSkipped(t *testing.T) {
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{skillRow("rss_reader", true)}}
	svc := newService(repo)
	main, global := newRegistries()

	err := svc.ApplyTo(main, global)
	require.NoError(t, err)
	assert.False(t, hasTool(main, "rss_reader"))
}

// 测试 5:重跑 ApplyTo 是幂等重建——先关后开/先开后关都收敛到正确状态,
// 且不碰注册在池里的框架工具(delegate 不属于 skill)。
func TestApplyTo_RebuildIdempotentAndKeepsFrameworkTools(t *testing.T) {
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{skillRow("calculator", true)}}
	svc := newService(repo)
	main, global := newRegistries()

	// 框架工具预注册在 main 池
	require.NoError(t, main.Register(agentpkg.Tool{Name: "delegate", Description: "framework"}))

	require.NoError(t, svc.ApplyTo(main, global))
	assert.True(t, hasTool(main, "calculator"))

	// 用户停用 → 重建 → 消失;框架工具不受影响
	require.NoError(t, repo.SetEnabled("calculator", false))
	require.NoError(t, svc.ApplyTo(main, global))
	assert.False(t, hasTool(main, "calculator"))
	assert.True(t, hasTool(main, "delegate"))

	// 重新开启 → 恢复
	require.NoError(t, repo.SetEnabled("calculator", true))
	require.NoError(t, svc.ApplyTo(main, global))
	assert.True(t, hasTool(main, "calculator"))
	assert.True(t, hasTool(main, "delegate"))
}

// 测试 6:SetEnabled 落库后立即应用(停用即时生效,无需重启)。
func TestSetEnabled_UpdatesRepoAndApplies(t *testing.T) {
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{skillRow("calculator", true)}}
	svc := newService(repo)
	main, global := newRegistries()
	require.NoError(t, svc.BindRegistries(main, global))
	require.NoError(t, svc.ApplyTo(main, global))
	require.True(t, hasTool(main, "calculator"))

	err := svc.SetEnabled("calculator", false)
	require.NoError(t, err)
	assert.Equal(t, "calculator", repo.enabledName)
	assert.False(t, repo.enabledValue)
	assert.False(t, hasTool(main, "calculator"))
	assert.False(t, hasTool(global, "calculator"))

	// 未绑定 registry 时直接调用也不报错(装配点尚未 Bind 的场景)
	svc2 := newService(repo)
	require.NoError(t, svc2.SetEnabled("calculator", true))
}

// 测试 7:List 返回视图,含来源与启停状态;执行体缺失的技能 Available=false(M1 builtin 恒 true)。
func TestList_ReturnsViews(t *testing.T) {
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{
		skillRow("get_current_time", true),
		skillRow("rss_reader", false), // 无 builder
	}}
	svc := newService(repo)

	views, err := svc.List()
	require.NoError(t, err)
	require.Len(t, views, 2)
	assert.Equal(t, "get_current_time", views[0].Name)
	assert.True(t, views[0].Enabled)
	assert.True(t, views[0].Available)
	assert.Equal(t, skilldomain.SourceBuiltin, views[0].Source)
	assert.Equal(t, "rss_reader", views[1].Name)
	assert.False(t, views[1].Enabled)
	assert.False(t, views[1].Available)
}
