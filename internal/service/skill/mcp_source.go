package skill

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/mark3labs/mcp-go/client"
	clienttransport "github.com/mark3labs/mcp-go/client/transport"
	mcp "github.com/mark3labs/mcp-go/mcp"

	skilldomain "omnibot/internal/domain/skill"
)

// MCPClient MCP 客户端窄接口(service 层声明;mark3labs/mcp-go 的 *client.Client 实现)。
// 测试以 mock 实现。
type MCPClient interface {
	Start(ctx context.Context) error
	Initialize(ctx context.Context, req mcp.InitializeRequest) (*mcp.InitializeResult, error)
	ListTools(ctx context.Context, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// MCPServerSpec 单个 MCP server 连接配置(调用前已解密;测试注入 mock factory)。
type MCPServerSpec struct {
	Name    string
	BaseURL string
	APIKey  string
	Enabled bool
	// OAuth(M4):AuthType=oauth 时走 OAuth 客户端(TokenStore 由 dbTokenStore 提供)。
	AuthType          string
	Transport         string // ""/streamable/sse
	OAuthClientID     string
	OAuthClientSecret string
	OAuthScopes       string
	TokenStore        clienttransport.TokenStore
}

// MCPClientFactory 由配置构造客户端(装配点注入真实实现,测试注入 mock)。
type MCPClientFactory func(spec MCPServerSpec) (MCPClient, error)

// NewStreamableHTTPMCPClient 真实客户端工厂:
//   - Transport=sse:    SSE 客户端(HTTP+SSE,2024-11 协议;高德等平台端点)
//   - AuthType=query:  APIKey 以 key=<key> 追加到 URL 参数(高德惯例),两种传输都支持
//   - 其余:            Streamable HTTP,APIKey 走 Bearer 头;oauth 走 OAuthHandler
func NewStreamableHTTPMCPClient(spec MCPServerSpec) (MCPClient, error) {
	if spec.AuthType == skilldomain.AuthTypeQuery {
		baseURL, err := withQueryParam(spec.BaseURL, "key", spec.APIKey)
		if err != nil {
			return nil, err
		}
		spec = MCPServerSpec{Name: spec.Name, BaseURL: baseURL, Enabled: spec.Enabled}
		if spec.BaseURL != "" && spec.Transport == skilldomain.TransportSSE {
			return newSSEMCPClient(spec)
		}
		return client.NewStreamableHttpClient(spec.BaseURL)
	}
	if spec.Transport == skilldomain.TransportSSE {
		return newSSEMCPClient(spec)
	}
	opts := []clienttransport.StreamableHTTPCOption{
		clienttransport.WithHTTPTimeout(MCPToolTimeout),
	}
	if spec.AuthType == skilldomain.AuthTypeOAuth {
		return client.NewOAuthStreamableHttpClient(spec.BaseURL, clienttransport.OAuthConfig{
			ClientID:     spec.OAuthClientID,
			ClientSecret: spec.OAuthClientSecret,
			RedirectURI:  "", // 连接阶段不需要;授权流程由 service 层的 handler 负责
			Scopes:       splitScopes(spec.OAuthScopes),
			TokenStore:   spec.TokenStore,
			PKCEEnabled:  true,
		}, opts...)
	}
	if spec.APIKey != "" {
		opts = append(opts, clienttransport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + spec.APIKey,
		}))
	}
	return client.NewStreamableHttpClient(spec.BaseURL, opts...)
}

// withQueryParam 把 key=<value> 追加到 URL query(已有同名参数则不覆盖)。
func withQueryParam(rawURL, key, value string) (string, error) {
	if value == "" {
		return rawURL, nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("地址解析失败: %w", err)
	}
	q := u.Query()
	if q.Get(key) == "" {
		q.Set(key, value)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

// newSSEMCPClient SSE 传输客户端。APIKey 同样走 Bearer 头(高德用 URL query key,
// 无需额外处理——key 已在 BaseURL 里)。
func newSSEMCPClient(spec MCPServerSpec) (MCPClient, error) {
	opts := []clienttransport.ClientOption{
		clienttransport.WithHTTPClient(&http.Client{Timeout: MCPToolTimeout}),
	}
	if spec.APIKey != "" {
		opts = append(opts, clienttransport.WithHeaders(map[string]string{
			"Authorization": "Bearer " + spec.APIKey,
		}))
	}
	return client.NewSSEMCPClient(spec.BaseURL, opts...)
}

// mcpExecutorName 技能名 → CallTool 工具名(M2 中两者一致;预留映射位)。

// MCPToolTimeout MCP 工具调用超时。
const MCPToolTimeout = 30 * time.Second
