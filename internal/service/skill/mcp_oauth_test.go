package skill

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	clienttransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skilldomain "omnibot/internal/domain/skill"
)

// newMockAuthServer 起一个模拟 OAuth 授权服务器:
//   /.well-known/oauth-authorization-server → 元数据(含注册端点)
//   /register  → 动态客户端注册
//   /token     → 授权码/刷新换 token
func newMockAuthServer(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		// 元数据 issuer 与请求 host 对齐(RFC 8414)
		base := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                 base,
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
			"registration_endpoint":  base + "/register",
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"client_id":"dyn-client","client_secret":"dyn-secret"}`))
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "at-123",
			"token_type":    "Bearer",
			"refresh_token": "rt-456",
			"expires_in":    3600,
			"scope":         r.FormValue("scope"),
		})
	})
	return httptest.NewServer(mux)
}

func oauthRow(tokens string) *skilldomain.MCPServer {
	return &skilldomain.MCPServer{
		ID: 1, Name: "github", BaseURL: "https://mcp.example.com",
		AuthType: skilldomain.AuthTypeOAuth, Enabled: true,
		OAuthScopes: "repo", OAuthTokens: tokens,
	}
}

// 测试 28:dbTokenStore 存取 roundtrip——token JSON 加密落库(enc: 前缀),读回解密。
func TestDBTokenStore_RoundtripEncrypted(t *testing.T) {
	serverRepo := newMockServerRepo()
	serverRepo.servers = append(serverRepo.servers, oauthRow(""))
	svc := NewSkillService(&mockSkillRepository{})
	svc.SetMCPServerRepository(serverRepo)

	store := svc.newDBTokenStore(1)
	require.NoError(t, store.SaveToken(&clienttransport.Token{
		AccessToken: "at-1", RefreshToken: "rt-1", ExpiresIn: 60,
	}))
	stored := serverRepo.servers[0].OAuthTokens
	assert.True(t, strings.HasPrefix(stored, secretPrefix), "token 必须加密落库")
	assert.NotContains(t, stored, "at-1")

	tok, err := store.GetToken()
	require.NoError(t, err)
	assert.Equal(t, "at-1", tok.AccessToken)
	assert.Equal(t, "rt-1", tok.RefreshToken)
}

// 测试 29:BeginOAuth 返回授权 URL(含 client_id/redirect_uri/PKCE/S256),挂起 state。
func TestBeginOAuth_BuildsAuthorizationURL(t *testing.T) {
	authSrv := newMockAuthServer(t)
	defer authSrv.Close()

	serverRepo := newMockServerRepo()
	serverRepo.servers = append(serverRepo.servers, oauthRow(""))
	svc := NewSkillService(&mockSkillRepository{})
	svc.SetMCPServerRepository(serverRepo)
	svc.SetOAuthRedirectBase("http://bot.local:8080")

	// 用本地 mock 授权服务器替换 BaseURL 以便元数据发现命中
	row := serverRepo.servers[0]
	row.BaseURL = authSrv.URL

	res, err := svc.BeginOAuth(context.Background(), 1)
	require.NoError(t, err)
	assert.Contains(t, res.AuthorizationURL, "/authorize?")
	assert.Contains(t, res.AuthorizationURL, "client_id=")
	assert.Contains(t, res.AuthorizationURL, "redirect_uri="+url.QueryEscape("http://bot.local:8080/api/v1/mcp/oauth/callback"))
	assert.Contains(t, res.AuthorizationURL, "code_challenge_method=S256")
	assert.Contains(t, res.AuthorizationURL, "scope=repo")

	svc.pendingMu.RLock()
	_, pending := svc.pendingOAuth[res.State]
	svc.pendingMu.RUnlock()
	assert.True(t, pending, "state 必须挂起等待回调")
}

// 测试 30:未填 client_id 时先走动态客户端注册,注册结果持久化(密文)。
func TestBeginOAuth_DynamicClientRegistration(t *testing.T) {
	authSrv := newMockAuthServer(t)
	defer authSrv.Close()

	serverRepo := newMockServerRepo()
	row := oauthRow("")
	row.BaseURL = authSrv.URL // client_id 为空 → 触发动态注册
	serverRepo.servers = append(serverRepo.servers, row)
	svc := NewSkillService(&mockSkillRepository{})
	svc.SetMCPServerRepository(serverRepo)
	svc.SetOAuthRedirectBase("http://bot.local:8080")

	_, err := svc.BeginOAuth(context.Background(), 1)
	require.NoError(t, err)
	updated, err := serverRepo.GetByID(1)
	require.NoError(t, err)
	assert.Equal(t, "dyn-client", updated.OAuthClientID)
	assert.True(t, strings.HasPrefix(updated.OAuthClientSecret, secretPrefix), "注册所得 secret 加密落库")
	assert.NotContains(t, updated.OAuthClientSecret, "dyn-secret")
}

// 测试 31:HandleOAuthCallback 有效 state → 换 token 加密落库,一次性消费 state。
func TestHandleOAuthCallback_ExchangesAndPersists(t *testing.T) {
	authSrv := newMockAuthServer(t)
	defer authSrv.Close()

	serverRepo := newMockServerRepo()
	row := oauthRow("")
	row.BaseURL = authSrv.URL
	serverRepo.servers = append(serverRepo.servers, row)
	svc := NewSkillService(&mockSkillRepository{})
	svc.SetMCPServerRepository(serverRepo)
	svc.SetOAuthRedirectBase("http://bot.local:8080")

	res, err := svc.BeginOAuth(context.Background(), 1)
	require.NoError(t, err)

	err = svc.HandleOAuthCallback(context.Background(), "auth-code-1", res.State)
	require.NoError(t, err)

	stored := serverRepo.servers[0].OAuthTokens
	assert.True(t, strings.HasPrefix(stored, secretPrefix))
	assert.NotContains(t, stored, "at-123")

	// state 一次性:重放被拒
	err = svc.HandleOAuthCallback(context.Background(), "auth-code-2", res.State)
	require.Error(t, err)
}

// 测试 32:无效/过期 state 回调被拒(CSRF 防护)。
func TestHandleOAuthCallback_InvalidState(t *testing.T) {
	serverRepo := newMockServerRepo()
	svc := NewSkillService(&mockSkillRepository{})
	svc.SetMCPServerRepository(serverRepo)

	err := svc.HandleOAuthCallback(context.Background(), "code", "bogus-state")
	require.Error(t, err)
}

// 测试 33:已授权 token 过期时连接——OAuthHandler 经 dbTokenStore 自动用 refresh_token 刷新。
func TestDBTokenStore_FeedsHandlerRefresh(t *testing.T) {
	authSrv := newMockAuthServer(t)
	defer authSrv.Close()

	serverRepo := newMockServerRepo()
	row := oauthRow("")
	row.BaseURL = authSrv.URL
	serverRepo.servers = append(serverRepo.servers, row)
	svc := NewSkillService(&mockSkillRepository{})
	svc.SetMCPServerRepository(serverRepo)

	store := svc.newDBTokenStore(1)
	// 写入一个已过期 token(带 refresh_token)
	expired := &clienttransport.Token{AccessToken: "old-at", RefreshToken: "rt-456", ExpiresIn: -10}
	expired.ExpiresAt = time.Now().Add(-time.Minute)
	require.NoError(t, store.SaveToken(expired))

	tok, err := svc.refreshTokenIfExpired(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, "at-123", tok.AccessToken, "应已通过 mock /token 端点刷新")
	assert.Equal(t, "rt-456", tok.RefreshToken)
}
