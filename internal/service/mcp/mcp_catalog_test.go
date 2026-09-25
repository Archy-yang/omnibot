package mcp

import (
	"context"
	"encoding/json"
	"testing"

	mcpdomain "omnibot/internal/domain/mcp"
	"omnibot/internal/pkg/toolcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMCPClient MCP 客户端桩:可注入工具清单与错误(接口迁移后 Start/Initialize 已内聚进工厂)。
type mockMCPClient struct {
	tools      []mcpdomain.MCPRemoteTool
	listErr    error
	callErr    error
	callResult mcpdomain.MCPToolCallResult
	closeCount int
}

func (c *mockMCPClient) ListTools(ctx context.Context) ([]mcpdomain.MCPRemoteTool, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	return c.tools, nil
}

func (c *mockMCPClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (mcpdomain.MCPToolCallResult, error) {
	if c.callErr != nil {
		return mcpdomain.MCPToolCallResult{}, c.callErr
	}
	return c.callResult, nil
}

func (c *mockMCPClient) Close() error { c.closeCount++; return nil }

func textTool(name, desc string) mcpdomain.MCPRemoteTool {
	return mcpdomain.MCPRemoteTool{Name: name, Description: desc, InputSchema: json.RawMessage(`{"type":"object"}`)}
}

// mockFactory 依次吐出预置客户端;记录连接次数(停用 server 不得发起连接)。
func mockFactory(clients ...*mockMCPClient) MCPClientFactory {
	i := 0
	return func(ctx context.Context, spec MCPServerSpec) (MCPClient, error) {
		if i < len(clients) {
			c := clients[i]
			i++
			return c, nil
		}
		return &mockMCPClient{}, nil
	}
}

// failFactory 永远失败的工厂(连接错误在工厂阶段抛出)。
func failFactory() MCPClientFactory {
	return func(ctx context.Context, spec MCPServerSpec) (MCPClient, error) {
		return nil, assert.AnError
	}
}

// 测试 16:目录化——replaceServerCatalog 填充内存目录,remove 即时失效;
// MCP 工具不进 tools 表(与 service/tool 分家)。
func TestMCPCatalog_ReplaceAndMatch(t *testing.T) {
	svc := NewMCPService()
	serverRow := &mcpdomain.MCPServer{ID: 1, Name: "github", Enabled: true}

	n := svc.replaceServerCatalog(serverRow, []mcpdomain.MCPRemoteTool{textTool("gh_search", "搜索 GitHub 仓库")})
	require.Equal(t, 1, n)

	svc.catalog.mu.RLock()
	tools := svc.catalog.byServer[1]
	svc.catalog.mu.RUnlock()
	require.Len(t, tools, 1)
	assert.Equal(t, "gh_search", tools[0].Name)
	assert.Equal(t, int64(0), tools[0].OwnerUserID, "共享行归属 0")
	assert.Contains(t, tools[0].ParamsSchema, `"type":"object"`, "InputSchema 原文进目录")

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
	var _ = toolcore.Tool{}
}
