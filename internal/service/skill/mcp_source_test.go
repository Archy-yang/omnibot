package skill

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcp "github.com/mark3labs/mcp-go/mcp"

	skilldomain "omnibot/internal/domain/skill"
)

// ---- mock MCP client ----

type mockMCPClient struct {
	startErr      error
	initErr       error
	listErr       error
	tools         []mcp.Tool
	callResult    *mcp.CallToolResult
	callErr       error
	callName      string
	callArguments any
	started       bool
	initialized   bool
}

func (c *mockMCPClient) Start(ctx context.Context) error {
	c.started = true
	return c.startErr
}

func (c *mockMCPClient) Initialize(ctx context.Context, req mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	c.initialized = true
	if c.initErr != nil {
		return nil, c.initErr
	}
	return &mcp.InitializeResult{}, nil
}

func (c *mockMCPClient) ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	return &mcp.ListToolsResult{Tools: c.tools}, nil
}

func (c *mockMCPClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	c.callName = req.Params.Name
	c.callArguments = req.Params.Arguments
	if c.callErr != nil {
		return nil, c.callErr
	}
	return c.callResult, nil
}

func textTool(name, desc string) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: desc,
		InputSchema: mcp.ToolInputSchema{Type: "object"},
	}
}

func textResult(text string) *mcp.CallToolResult {
	return mcp.NewToolResultText(text)
}

func mockFactory(clients ...*mockMCPClient) MCPClientFactory {
	i := 0
	return func(spec MCPServerSpec) (MCPClient, error) {
		if i >= len(clients) {
			i = len(clients) - 1 // 复用最后一个 client(多次同步场景)
		}
		c := clients[i]
		i++
		return c, nil
	}
}

func mcpRow(name, server string, enabled bool) *skilldomain.Skill {
	return &skilldomain.Skill{
		Name:        name,
		DisplayName: name,
		Description: "remote " + name,
		Source:      skilldomain.SourceMCP,
		Enabled:     enabled,
		MainVisible: true,
		MCPServer:   server,
		ParamsSchema: `{"type":"object","properties":{}}`,
	}
}

// ---- SyncMCPServers ----

// 测试 8:发现的远端工具落库,source=mcp,默认停用,记录所属 server。
func TestSyncMCPServers_DiscoveredToolsDefaultDisabled(t *testing.T) {
	repo := &mockSkillRepository{}
	svc := newService(repo)
	client := &mockMCPClient{tools: []mcp.Tool{textTool("gh_search", "搜 GitHub")}}
	svc.SetMCPClientFactory(mockFactory(client))

	err := svc.SyncMCPServers(context.Background(), []MCPServerSpec{
		{Name: "github", BaseURL: "https://mcp.example.com", APIKey: "sk-1", Enabled: true},
	})
	require.NoError(t, err)
	require.Len(t, repo.upsertedMCP, 1)

	def := repo.upsertedMCP[0]
	assert.Equal(t, "gh_search", def.Name)
	assert.Equal(t, "github", def.MCPServer)
	assert.Equal(t, "搜 GitHub", def.Description)
	assert.False(t, def.Enabled, "发现的远端技能默认停用,须用户逐个开启")
	assert.True(t, def.MainVisible)
}

// 测试 9:连接失败不阻塞启动、不报错,其他 server 继续同步。
func TestSyncMCPServers_ConnectFailureNonBlocking(t *testing.T) {
	repo := &mockSkillRepository{}
	svc := newService(repo)
	bad := &mockMCPClient{startErr: errors.New("connection refused")}
	good := &mockMCPClient{tools: []mcp.Tool{textTool("ok_tool", "ok")}}
	svc.SetMCPClientFactory(mockFactory(bad, good))

	err := svc.SyncMCPServers(context.Background(), []MCPServerSpec{
		{Name: "broken", BaseURL: "http://x", Enabled: true},
		{Name: "fine", BaseURL: "http://y", Enabled: true},
	})
	require.NoError(t, err)
	require.Len(t, repo.upsertedMCP, 1)
	assert.Equal(t, "ok_tool", repo.upsertedMCP[0].Name)
}

// 测试 10:与内置技能重名的远端工具跳过(不覆盖内置),其余正常入库。
func TestSyncMCPServers_ConflictWithBuiltinSkipped(t *testing.T) {
	repo := &mockSkillRepository{}
	svc := newService(repo)
	client := &mockMCPClient{tools: []mcp.Tool{
		textTool("calculator", "远端假计算器"),
		textTool("mcp_only", "独有工具"),
	}}
	svc.SetMCPClientFactory(mockFactory(client))

	err := svc.SyncMCPServers(context.Background(), []MCPServerSpec{
		{Name: "github", BaseURL: "http://x", Enabled: true},
	})
	require.NoError(t, err)
	require.Len(t, repo.upsertedMCP, 1)
	assert.Equal(t, "mcp_only", repo.upsertedMCP[0].Name)
}

// 测试 11:重复同步(重启)保留用户启停状态——repo 契约由 UpsertMCPTool 保证,此处验证传递路径。
func TestSyncMCPServers_ResyncReachesRepo(t *testing.T) {
	repo := &mockSkillRepository{}
	svc := newService(repo)
	svc.SetMCPClientFactory(mockFactory(&mockMCPClient{tools: []mcp.Tool{textTool("a1", "x")}}))

	require.NoError(t, svc.SyncMCPServers(context.Background(), []MCPServerSpec{
		{Name: "s", BaseURL: "http://x", Enabled: true},
	}))
	require.NoError(t, svc.SyncMCPServers(context.Background(), []MCPServerSpec{
		{Name: "s", BaseURL: "http://x", Enabled: true},
	}))
	assert.Len(t, repo.upsertedMCP, 2)
}

// 测试 12:从配置移除的 server,其技能行被清理(不再出现在清单)。
func TestSyncMCPServers_RemovedServerCleanup(t *testing.T) {
	repo := &mockSkillRepository{}
	repo.rows = []*skilldomain.Skill{mcpRow("old_tool", "removed-server", true)}
	svc := newService(repo)
	svc.SetMCPClientFactory(mockFactory(&mockMCPClient{tools: []mcp.Tool{textTool("new_tool", "n")}}))

	require.NoError(t, svc.SyncMCPServers(context.Background(), []MCPServerSpec{
		{Name: "alive", BaseURL: "http://x", Enabled: true},
	}))
	// repo 收到的是"配置内 server 名集合",删除语义(NOT IN)由 repo 层测试保证
	require.Equal(t, []string{"alive"}, repo.deletedNotIn)
}

// 测试 13:enabled=false 的 server 不同步、不连接。
func TestSyncMCPServers_DisabledServerSkipped(t *testing.T) {
	repo := &mockSkillRepository{}
	svc := newService(repo)
	client := &mockMCPClient{tools: []mcp.Tool{textTool("t", "x")}}
	factory := mockFactory(client)
	svc.SetMCPClientFactory(factory)

	require.NoError(t, svc.SyncMCPServers(context.Background(), []MCPServerSpec{
		{Name: "off", BaseURL: "http://x", Enabled: false},
	}))
	assert.Empty(t, repo.upsertedMCP)
	assert.False(t, client.started, "停用的 server 不得发起任何连接(未开启技能不外发数据)")
}

// ---- mcp 执行体 ----

// 测试 14:同步后 mcp 技能可进运行时池,Execute 走 CallTool 并抽取文本内容。
func TestMCPSkill_AppliedAndExecutes(t *testing.T) {
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{mcpRow("gh_search", "github", true)}}
	svc := newService(repo)
	client := &mockMCPClient{
		tools:      []mcp.Tool{textTool("gh_search", "搜 GitHub")},
		callResult: textResult(`{"result": 42}`),
	}
	svc.SetMCPClientFactory(mockFactory(client))
	require.NoError(t, svc.SyncMCPServers(context.Background(), []MCPServerSpec{
		{Name: "github", BaseURL: "http://x", Enabled: true},
	}))

	main, global := newRegistries()
	require.NoError(t, svc.ApplyTo(main, global))
	require.True(t, hasTool(global, "gh_search"))

	tool, ok := global.Get("gh_search")
	require.True(t, ok)
	out, err := tool.Execute(context.Background(), map[string]interface{}{"q": "omnibot"})
	require.NoError(t, err)
	assert.Equal(t, `{"result": 42}`, out)
	assert.Equal(t, "gh_search", client.callName)
	args, _ := client.callArguments.(map[string]any)
	assert.Equal(t, "omnibot", args["q"])
}

// 测试 15:server 下线后(无执行体)技能隐藏——不进池,不报错。
func TestMCPSkill_MissingExecutorHidden(t *testing.T) {
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{mcpRow("gh_search", "github", true)}}
	svc := newService(repo) // 未同步 → 无执行体

	main, global := newRegistries()
	require.NoError(t, svc.ApplyTo(main, global))
	assert.False(t, hasTool(global, "gh_search"))
}

// 测试 16:远端工具报错(IsError)时,执行体把文本作为错误返回,助手可如实告知用户。
func TestMCPSkill_IsErrorSurfacesAsError(t *testing.T) {
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{mcpRow("gh_search", "github", true)}}
	svc := newService(repo)
	client := &mockMCPClient{
		tools:      []mcp.Tool{textTool("gh_search", "搜 GitHub")},
		callResult: textResult("rate limited"),
	}
	client.callResult.IsError = true
	svc.SetMCPClientFactory(mockFactory(client))
	require.NoError(t, svc.SyncMCPServers(context.Background(), []MCPServerSpec{
		{Name: "github", BaseURL: "http://x", Enabled: true},
	}))

	main, global := newRegistries()
	require.NoError(t, svc.ApplyTo(main, global))
	tool, ok := global.Get("gh_search")
	require.True(t, ok, "mcp 技能应已进池")
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate limited")
}

// 测试 17:schema 不可用的 mcp 行照常入库,但 ApplyTo 时隐藏(schema 不可用)。
// (直接注入执行体,隔离 schema 路径;执行体缺失路径由 MissingExecutorHidden 覆盖。)
func TestMCPSkill_InvalidSchemaRowKeptButHiddenOnApply(t *testing.T) {
	row := mcpRow("bad_schema", "github", true)
	row.ParamsSchema = "{invalid-json"
	repo := &mockSkillRepository{rows: []*skilldomain.Skill{row}}
	svc := newService(repo)
	svc.registerMCPExecutor("bad_schema", func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "should not be called", nil
	})

	main, global := newRegistries()
	require.NoError(t, svc.ApplyTo(main, global))
	assert.False(t, hasTool(global, "bad_schema"))
}

// schema 序列化:ToolInputSchema → JSON 字符串(repo 存 ParamsSchema 用)。
func TestToolSchemaToJSON(t *testing.T) {
	b, err := json.Marshal(mcp.ToolInputSchema{Type: "object"})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"type":"object"`)
}
