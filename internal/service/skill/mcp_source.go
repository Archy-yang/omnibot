package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	clienttransport "github.com/mark3labs/mcp-go/client/transport"
	mcp "github.com/mark3labs/mcp-go/mcp"

	skilldomain "omnibot/internal/domain/skill"
	agentpkg "omnibot/internal/service/agent"
)

// MCPClient MCP 客户端窄接口(service 层声明;mark3labs/mcp-go 的 *client.Client 实现)。
// 测试以 mock 实现。
type MCPClient interface {
	Start(ctx context.Context) error
	Initialize(ctx context.Context, req mcp.InitializeRequest) (*mcp.InitializeResult, error)
	ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// MCPServerSpec 单个 MCP server 配置(来自 config.yaml,系统级;密钥不落库)。
type MCPServerSpec struct {
	Name    string
	BaseURL string
	APIKey  string
	Enabled bool
}

// MCPClientFactory 由配置构造客户端(装配点注入真实实现,测试注入 mock)。
type MCPClientFactory func(spec MCPServerSpec) (MCPClient, error)

// NewStreamableHTTPMCPClient 真实客户端工厂:Streamable HTTP 传输,APIKey 走 Bearer 头。
func NewStreamableHTTPMCPClient(spec MCPServerSpec) (MCPClient, error) {
	opts := []clienttransport.StreamableHTTPCOption{
		clienttransport.WithHTTPTimeout(MCPToolTimeout),
	}
	if spec.APIKey != "" {
		opts = append(opts, clienttransport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + spec.APIKey,
		}))
	}
	return client.NewStreamableHttpClient(spec.BaseURL, opts...)
}

// mcpExecutorName 技能名 → CallTool 工具名(M2 中两者一致;预留映射位)。
type mcpExecutor func(ctx context.Context, args map[string]interface{}) (string, error)

// SetMCPClientFactory 注入客户端工厂(装配/测试用)。
func (s *SkillService) SetMCPClientFactory(f MCPClientFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpFactory = f
}

// SyncMCPServers 与配置的 MCP server 同步:发现工具 → upsert 技能(默认停用)→ 注册执行体。
// 单个 server 失败不阻塞整体(13-技术方案 §6.2):该 server 技能隐藏,助手口径"没有这个技能"。
// 从 spec 列表移除的 server,其技能行被清理。
func (s *SkillService) SyncMCPServers(ctx context.Context, specs []MCPServerSpec) error {
	// 收集所有配置内 server 名,清理已移除 server 的技能行
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	if _, err := s.repo.DeleteMCPSkillsNotIn(names); err != nil {
		return fmt.Errorf("skill: cleanup removed mcp servers: %w", err)
	}

	for _, spec := range specs {
		if !spec.Enabled {
			continue // 停用的 server 不发起任何连接(未开启技能不外发数据,PRD 4.4)
		}
		s.syncOneServer(ctx, spec)
	}
	return nil
}

// syncOneServer 同步单个 server;失败记日志并返回(不阻塞其他 server)。
func (s *SkillService) syncOneServer(ctx context.Context, spec MCPServerSpec) {
	s.mu.RLock()
	factory := s.mcpFactory
	s.mu.RUnlock()
	if factory == nil {
		return
	}

	mcpClient, err := factory(spec)
	if err != nil {
		fmt.Printf("[skill] mcp server %q client create failed: %v\n", spec.Name, err)
		return
	}
	if err := mcpClient.Start(ctx); err != nil {
		fmt.Printf("[skill] mcp server %q start failed: %v\n", spec.Name, err)
		return
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "omnibot", Version: "1.0"}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		fmt.Printf("[skill] mcp server %q initialize failed: %v\n", spec.Name, err)
		return
	}
	result, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		fmt.Printf("[skill] mcp server %q list tools failed: %v\n", spec.Name, err)
		return
	}

	for _, tool := range result.Tools {
		// 与内置技能重名 → 跳过(不覆盖内置,13-技术方案 §4)
		s.mu.RLock()
		_, conflict := s.builders[tool.Name]
		s.mu.RUnlock()
		if conflict {
			fmt.Printf("[skill] mcp tool %q (server %s) conflicts with builtin, skipped\n", tool.Name, spec.Name)
			continue
		}

		schemaJSON, _ := json.Marshal(tool.InputSchema)
		def := skilldomain.MCPToolDef{
			Name:         tool.Name,
			DisplayName:  tool.Name,
			Description:  tool.Description,
			MCPServer:    spec.Name,
			ParamsSchema: string(schemaJSON),
			MainVisible:  true, // 远端技能默认主 Agent 也可用(抓取类限制只针对内置)
			Enabled:      false,
		}
		if err := s.repo.UpsertMCPTool(def); err != nil {
			fmt.Printf("[skill] mcp tool %q upsert failed: %v\n", tool.Name, err)
			continue
		}
		s.registerMCPExecutor(tool.Name, makeMCPToolExecutor(mcpClient, tool.Name))
	}
}

// registerMCPExecutor 注册 mcp 技能执行体(技能名 → CallTool 闭包)。
func (s *SkillService) registerMCPExecutor(name string, exec mcpExecutor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpExecutors[name] = exec
}

// makeMCPToolExecutor 构造单工具执行闭包:CallTool + 文本内容抽取 + 30s 超时。
func makeMCPToolExecutor(mcpClient MCPClient, toolName string) mcpExecutor {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		callCtx, cancel := context.WithTimeout(ctx, MCPToolTimeout)
		defer cancel()

		req := mcp.CallToolRequest{}
		req.Params.Name = toolName
		if args != nil {
			req.Params.Arguments = args
		}
		result, err := mcpClient.CallTool(callCtx, req)
		if err != nil {
			return "", fmt.Errorf("技能暂时不可用(%s): %w", toolName, err)
		}
		if result.IsError {
			return "", fmt.Errorf("技能调用失败(%s): %s", toolName, mcpContentText(result))
		}
		return mcpContentText(result), nil
	}
}

// mcpContentText 抽取 CallToolResult 的文本内容(多个 TextContent 换行拼接)。
func mcpContentText(result *mcp.CallToolResult) string {
	var texts []string
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	if len(texts) == 0 {
		return "(无文本内容)"
	}
	return strings.Join(texts, "\n")
}

// buildMCPTool 由 skill 行 + mcp 执行体构造运行时 Tool。
// 远端定义以库内行为准(同步时来自 ListTools);执行体缺失或 schema 非法 → false(隐藏)。
func (s *SkillService) buildMCPTool(row *skilldomain.Skill) (agentpkg.Tool, bool) {
	s.mu.RLock()
	exec, ok := s.mcpExecutors[row.Name]
	s.mu.RUnlock()
	if !ok {
		return agentpkg.Tool{}, false
	}
	schema, ok := skilldomain.UnmarshalSchema(row.ParamsSchema)
	if !ok {
		return agentpkg.Tool{}, false
	}
	return agentpkg.Tool{
		Name:         row.Name,
		Description:  row.Description,
		DisplayLabel: row.DisplayName,
		Capabilities: skilldomain.SplitCapabilities(row.Capabilities),
		Parameters:   schema,
		Execute:      exec,
	}, true
}

// MCPToolTimeout MCP 工具调用超时。
const MCPToolTimeout = 30 * time.Second
