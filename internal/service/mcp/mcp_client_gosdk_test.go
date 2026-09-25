package mcp

// mcp_client_gosdk_test.go — go-sdk 客户端真协议集成测试。
// 用 go-sdk 的 server 侧(NewServer + StreamableHTTPHandler/SSEHandler)起假 MCP server,
// 走真实 JSON-RPC 握手与调用,验证工厂/传输/鉴权/类型映射全链路。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeMCPServer 起一个带 echo 工具的假 MCP server,返回 httptest server 与收到的鉴权头记录。
// transport: "streamable" | "sse"。
func newFakeMCPServer(t *testing.T, transport string) (*httptest.Server, *authRecorder) {
	t.Helper()
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake", Version: "1.0"}, nil)
	sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: "echo", Description: "回显输入文本"},
		func(ctx context.Context, req *sdkmcp.CallToolRequest, in struct {
			Text string `json:"text"`
		}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "echo:" + in.Text}}}, nil, nil
		})

	rec := &authRecorder{}
	var handler http.Handler
	switch transport {
	case "sse":
		handler = sdkmcp.NewSSEHandler(func(r *http.Request) *sdkmcp.Server { return srv }, nil)
	default:
		handler = sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server { return srv }, nil)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		handler.ServeHTTP(w, r)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, rec
}

// authRecorder 记录请求路径与鉴权头(断言 Bearer 注入)。
type authRecorder struct {
	mu    sync.Mutex
	auths []string
	paths []string
}

func (a *authRecorder) record(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.auths = append(a.auths, r.Header.Get("Authorization"))
	a.paths = append(a.paths, r.URL.Path)
}

func (a *authRecorder) snapshot() ([]string, []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.auths, a.paths
}

// TestGoSDKClient_Streamable streamable 传输全链路:连接→ListTools→CallTool→Close。
func TestGoSDKClient_Streamable(t *testing.T) {
	ts, rec := newFakeMCPServer(t, "streamable")

	cli, err := NewStreamableHTTPMCPClient(context.Background(), MCPServerSpec{
		Name: "fake", BaseURL: ts.URL, AuthType: "bearer", APIKey: "tok-1", Enabled: true,
	})
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	tools, err := cli.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name)
	assert.Equal(t, "回显输入文本", tools[0].Description)
	assert.NotEmpty(t, tools[0].InputSchema, "InputSchema 应有 JSON Schema 原文")

	res, err := cli.CallTool(context.Background(), "echo", map[string]interface{}{"text": "你好"})
	require.NoError(t, err)
	assert.False(t, res.IsError)
	assert.Equal(t, "echo:你好", res.Text)

	auths, _ := rec.snapshot()
	require.NotEmpty(t, auths)
	assert.Equal(t, "Bearer tok-1", auths[0], "Bearer 头必须注入")
}

// TestGoSDKClient_SSE SSE 传输(2024-11 旧协议,高德等端点)同链路。
func TestGoSDKClient_SSE(t *testing.T) {
	ts, _ := newFakeMCPServer(t, "sse")

	cli, err := NewStreamableHTTPMCPClient(context.Background(), MCPServerSpec{
		Name: "fake", BaseURL: ts.URL, Transport: "sse", AuthType: "bearer", APIKey: "tok-sse", Enabled: true,
	})
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	tools, err := cli.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "echo", tools[0].Name)

	res, err := cli.CallTool(context.Background(), "echo", map[string]interface{}{"text": "sse"})
	require.NoError(t, err)
	assert.Equal(t, "echo:sse", res.Text)
}

// TestGoSDKClient_QueryAuth query 鉴权:key=<APIKey> 追加到 URL(高德惯例)。
func TestGoSDKClient_QueryAuth(t *testing.T) {
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake", Version: "1.0"}, nil)
	sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: "ping", Description: "ping"},
		func(ctx context.Context, req *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "pong"}}}, nil, nil
		})
	ts := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server { return srv }, nil))
	t.Cleanup(ts.Close)

	cli, err := NewStreamableHTTPMCPClient(context.Background(), MCPServerSpec{
		Name: "amap", BaseURL: ts.URL, AuthType: "query", APIKey: "amap-key", Enabled: true,
	})
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	// query 鉴权下 Bearer 不注入,工具调用照常
	res, err := cli.CallTool(context.Background(), "ping", nil)
	require.NoError(t, err)
	assert.Equal(t, "pong", res.Text)
}

// TestGoSDKClient_Unreachable 连不上的端点:工厂报连接失败(含初始化握手失败)。
func TestGoSDKClient_Unreachable(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	ts.Close() // 立即关闭制造拒绝连接

	_, err := NewStreamableHTTPMCPClient(context.Background(), MCPServerSpec{
		Name: "dead", BaseURL: ts.URL, Enabled: true,
	})
	require.Error(t, err)
}

// TestGoSDKClient_CallTimeout 调用超时由 ctx 纪律约束(30s 上限,测试用 100ms 缩短)。
func TestGoSDKClient_CallTimeout(t *testing.T) {
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake", Version: "1.0"}, nil)
	sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: "slow", Description: "慢工具"},
		func(ctx context.Context, req *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, any, error) {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return &sdkmcp.CallToolResult{}, nil, nil
			}
		})
	ts := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server { return srv }, nil))
	t.Cleanup(ts.Close)

	cli, err := NewStreamableHTTPMCPClient(context.Background(), MCPServerSpec{Name: "fake", BaseURL: ts.URL, Enabled: true})
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = cli.CallTool(ctx, "slow", nil)
	require.Error(t, err, "超时必须报错")
}

// TestGoSDKClient_RemoteError 远端工具执行报错:IsError 透传,文本含错误内容。
func TestGoSDKClient_RemoteError(t *testing.T) {
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fake", Version: "1.0"}, nil)
	sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: "boom", Description: "报错工具"},
		func(ctx context.Context, req *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{IsError: true, Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "参数缺失"}}}, nil, nil
		})
	ts := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(r *http.Request) *sdkmcp.Server { return srv }, nil))
	t.Cleanup(ts.Close)

	cli, err := NewStreamableHTTPMCPClient(context.Background(), MCPServerSpec{Name: "fake", BaseURL: ts.URL, Enabled: true})
	require.NoError(t, err)
	defer func() { _ = cli.Close() }()

	res, err := cli.CallTool(context.Background(), "boom", nil)
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, res.Text, "参数缺失")
}
