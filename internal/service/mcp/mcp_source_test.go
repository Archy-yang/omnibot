package mcp

import (
	"context"
	"testing"

	mcp "github.com/mark3labs/mcp-go/mcp"

	mcpdomain "omnibot/internal/domain/mcp"
	agentpkg "omnibot/internal/service/agent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMCPClient MCP 客户端桩:可注入 Start/初始化错误与工具清单。
type mockMCPClient struct {
	startErr error
	initErr  error
	tools    []mcp.Tool
	started  bool
}

func (c *mockMCPClient) Start(ctx context.Context) error { c.started = true; return c.startErr }

func (c *mockMCPClient) Initialize(ctx context.Context, req mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	return &mcp.InitializeResult{}, nil
}

func (c *mockMCPClient) ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{Tools: c.tools}, nil
}

func (c *mockMCPClient) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{}, nil
}

func textTool(name, desc string) mcp.Tool {
	return mcp.NewTool(name, mcp.WithDescription(desc))
}

func mockFactory(clients ...*mockMCPClient) MCPClientFactory {
	i := 0
	return func(spec MCPServerSpec) (MCPClient, error) {
		if i < len(clients) {
			c := clients[i]
			i++
			return c, nil
		}
		return &mockMCPClient{}, nil
	}
}

// 测试 16:目录化——replaceServerCatalog 填充内存目录,remove 即时失效;
// MCP 工具不进 tools 表(与 service/tool 分家)。
func TestMCPCatalog_ReplaceAndMatch(t *testing.T) {
	svc := NewMCPService()
	serverRow := &mcpdomain.MCPServer{ID: 1, Name: "github", Enabled: true}

	n := svc.replaceServerCatalog(serverRow, []mcp.Tool{textTool("gh_search", "搜索 GitHub 仓库")})
	require.Equal(t, 1, n)

	svc.catalog.mu.RLock()
	tools := svc.catalog.byServer[1]
	svc.catalog.mu.RUnlock()
	require.Len(t, tools, 1)
	assert.Equal(t, "gh_search", tools[0].Name)
	assert.Equal(t, int64(0), tools[0].OwnerUserID, "共享行归属 0")

	// 删除/停用 → 目录失效
	svc.catalog.remove(1)
	svc.catalog.mu.RLock()
	_, had := svc.catalog.byServer[1]
	svc.catalog.mu.RUnlock()
	assert.False(t, had)
}

// 测试 17:mcp_call / mcp_search 注册为普通 registry 工具(恒定注入)。
func TestMCPMetaTools_RegisteredAsTools(t *testing.T) {
	svc := NewMCPService()
	call := svc.CreateMCPCallTool()
	search := svc.CreateMCPSearchTool()
	assert.Equal(t, "mcp_call", call.Name)
	assert.Equal(t, "mcp_search", search.Name)
	assert.Contains(t, call.Description, "mcp_search")
	assert.Contains(t, search.Description, "3 次", "描述必须写明每回合搜索上限")
	var _ = agentpkg.Tool{}
}
