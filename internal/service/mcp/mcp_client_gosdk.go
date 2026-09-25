package mcp

// mcp_client_gosdk.go — MCP 客户端实现,基于官方 SDK(modelcontextprotocol/go-sdk)。
//
// 迁移自 mark3labs/mcp-go(2026-09-25 拍板:改用官方 SDK):
//   - 会话由 SDK 的 Connect 一步建立(握手内聚),不再有 Start/Initialize 两步;
//   - 传输:StreamableClientTransport(现行) + SSEClientTransport(2024-11 旧协议,高德等);
//   - 鉴权:query(key=URL 参数,高德惯例)/bearer(OAuth access token 同走 Bearer);
//   - service 层 MCPClient 窄接口以 domain/mcp 自有 DTO 收敛,SDK 类型不出本文件。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpdomain "omnibot/internal/domain/mcp"
)

// MCPClient MCP 客户端窄接口(service 层声明;goSDKClient 实现,测试以 mock 实现)。
type MCPClient interface {
	ListTools(ctx context.Context) ([]mcpdomain.MCPRemoteTool, error)
	CallTool(ctx context.Context, name string, args map[string]interface{}) (mcpdomain.MCPToolCallResult, error)
	Close() error
}

// MCPServerSpec 单个 MCP server 连接配置(调用前已解密;测试注入 mock factory)。
// AuthType=oauth 时 APIKey 字段承载 access token(连接前由 service 层刷新并解密填入)。
type MCPServerSpec struct {
	Name    string
	BaseURL string
	APIKey  string
	Enabled bool
	// OAuth(M4):授权流程由 service 层 handler 负责;此处只消费换得的 access token。
	AuthType          string
	Transport         string // ""/streamable/sse
	OAuthClientID     string
	OAuthClientSecret string
	OAuthScopes       string
}

// MCPClientFactory 由配置构造客户端(装配点注入真实实现,测试注入 mock)。
// Connect(含协议握手)在工厂内完成,返回即可用客户端。
type MCPClientFactory func(ctx context.Context, spec MCPServerSpec) (MCPClient, error)

// NewStreamableHTTPMCPClient 真实客户端工厂:
//   - Transport=sse:   SSE 客户端(HTTP+SSE,2024-11 协议;高德等平台端点)
//   - AuthType=query:  APIKey 以 key=<key> 追加到 URL 参数(高德惯例),两种传输都支持
//   - 其余:            Streamable HTTP,APIKey 走 Bearer 头
func NewStreamableHTTPMCPClient(ctx context.Context, spec MCPServerSpec) (MCPClient, error) {
	if spec.AuthType == mcpdomain.AuthTypeQuery {
		baseURL, err := withQueryParam(spec.BaseURL, "key", spec.APIKey)
		if err != nil {
			return nil, err
		}
		spec = MCPServerSpec{Name: spec.Name, BaseURL: baseURL, Enabled: spec.Enabled}
	}

	if spec.Transport == mcpdomain.TransportSSE {
		return newSSEMCPClient(spec)
	}

	transport := &sdkmcp.StreamableClientTransport{
		Endpoint: spec.BaseURL,
		// Timeout 0:streamable 有常驻 standalone SSE 流,不能用 client.Timeout 一刀切;
		// 超时纪律由调用方 ctx 负责(invokeMCPTool 的 30s callCtx / 同步处显式超时)。
		HTTPClient: &http.Client{Transport: bearerRoundTripper(spec.APIKey, nil)},
	}
	return connectGoSDKClient(ctx, transport)
}

// newSSEMCPClient SSE 传输客户端(2024-11 旧协议)。APIKey 走 Bearer 头
// (高德用 URL query key,无需额外处理——key 已在 BaseURL 里)。
func newSSEMCPClient(spec MCPServerSpec) (MCPClient, error) {
	transport := &sdkmcp.SSEClientTransport{
		Endpoint: spec.BaseURL,
		HTTPClient: &http.Client{
			Timeout:   MCPToolTimeout,
			Transport: bearerRoundTripper(spec.APIKey, nil),
		},
	}
	return connectGoSDKClient(context.Background(), transport)
}

// connectGoSDKClient 建 SDK 客户端并完成连接+协议握手。
func connectGoSDKClient(ctx context.Context, transport sdkmcp.Transport) (MCPClient, error) {
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "omnibot", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("连接失败: %w", err)
	}
	return &goSDKClient{session: session}, nil
}

// goSDKClient MCPClient 的 go-sdk 实现(类型映射收敛于此)。
type goSDKClient struct {
	session *sdkmcp.ClientSession
}

func (c *goSDKClient) ListTools(ctx context.Context) ([]mcpdomain.MCPRemoteTool, error) {
	res, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	out := make([]mcpdomain.MCPRemoteTool, 0, len(res.Tools))
	for _, t := range res.Tools {
		schema := []byte("{}")
		if t.InputSchema != nil {
			if b, err := json.Marshal(t.InputSchema); err == nil {
				schema = b
			}
		}
		out = append(out, mcpdomain.MCPRemoteTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out, nil
}

func (c *goSDKClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (mcpdomain.MCPToolCallResult, error) {
	params := &sdkmcp.CallToolParams{Name: name}
	if args != nil {
		params.Arguments = args
	}
	res, err := c.session.CallTool(ctx, params)
	if err != nil {
		return mcpdomain.MCPToolCallResult{}, err
	}
	return mcpdomain.MCPToolCallResult{Text: sdkContentText(res.Content), IsError: res.IsError}, nil
}

func (c *goSDKClient) Close() error {
	return c.session.Close()
}

// sdkContentText 抽取 SDK Content 列表的文本(只关心 TextContent)。
func sdkContentText(contents []sdkmcp.Content) string {
	var sb strings.Builder
	for _, c := range contents {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// bearerRoundTripper 注入 Bearer 头(空 token 直通)。
func bearerRoundTripper(bearer string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if bearer == "" {
		return base
	}
	return &headerRoundTripper{base: base, header: "Authorization", value: "Bearer " + bearer}
}

// headerRoundTripper 为每个请求附加固定头(鉴权头注入点)。
type headerRoundTripper struct {
	base   http.RoundTripper
	header string
	value  string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 克隆请求,避免复用连接时头部污染
	req2 := req.Clone(req.Context())
	req2.Header.Set(h.header, h.value)
	return h.base.RoundTrip(req2)
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

// MCPToolTimeout MCP 工具调用超时。
const MCPToolTimeout = 30 * time.Second
