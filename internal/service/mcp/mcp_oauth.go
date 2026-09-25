package mcp

// mcp_oauth.go — MCP OAuth 鉴权(自有实现,迁移自 mark3labs clienttransport)。
//
// 流程与 API 表面保持不变(BeginOAuth → 浏览器授权 → HandleOAuthCallback,
// token 加密落库 mcp_servers.oauth_tokens),仅底层实现替换:
//   - PKCE(state/verifier/challenge)自实现(crypto/rand + SHA-256 S256);
//   - 元数据发现:RFC 8414(.well-known/oauth-authorization-server);
//   - 动态客户端注册:go-sdk sdkoauthex.RegisterClient(RFC 7591);
//   - 换 token/刷新:标准 OAuth2 POST(自实现,~60 行);
//   - Token JSON 字段与原 mark3labs clienttransport.Token 完全一致,存量加密 token
//     直接可解析,已授权连接器无需重新授权。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdkoauthex "github.com/modelcontextprotocol/go-sdk/oauthex"

	mcpdomain "omnibot/internal/domain/mcp"
)

// oauthCallbackPath OAuth 回调固定路径(与 routes 注册、服务商登记的 redirect_uri 一致)。
const oauthCallbackPath = "/api/v1/mcp/oauth/callback"

// oauthPendingTTL 授权流程挂起时长(state + PKCE verifier 的有效期)。
const oauthPendingTTL = 10 * time.Minute

// OAuthBeginResult 发起授权的结果:前端打开 AuthorizationURL 完成授权。
type OAuthBeginResult struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
}

// pendingOAuth 一次进行中的授权流程(state → 上下文)。
type pendingOAuth struct {
	ServerID  int64
	Verifier  string // PKCE code_verifier(回调换 token 时用)
	ExpiresAt time.Time
}

// Token OAuth 令牌(JSON 字段与原 mark3labs clienttransport.Token 一致,存量兼容)。
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresIn    int64     `json:"expires_in,omitempty"`
	Scope        string    `json:"scope,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

// IsExpired token 是否已过期(提前 30s 余量)。
func (t *Token) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false // 无过期时间视为长期有效
	}
	return time.Now().After(t.ExpiresAt.Add(-30 * time.Second))
}

// oauthEndpoints RFC 8414 授权服务器元数据(只取用到的字段)。
type oauthEndpoints struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint,omitempty"`
}

// oauthTokenStore token 持久化窄接口(dbTokenStore 实现)。
type oauthTokenStore interface {
	GetToken() (*Token, error)
	SaveToken(*Token) error
}

// SetOAuthRedirectBase 设置 OAuth 回调基址(如 https://bot.example.com)。
// 完整 redirect_uri = <base>/api/v1/mcp/oauth/callback,须与服务商登记一致。
func (s *MCPService) SetOAuthRedirectBase(base string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauthRedirectBase = strings.TrimRight(base, "/")
}

// oauthRedirectURI 完整回调地址。
func (s *MCPService) oauthRedirectURI() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.oauthRedirectBase + oauthCallbackPath
}

// newDBTokenStore 构造绑定指定 server 的 DB token 存储:
// token JSON 整体 AES 加密落 mcp_servers.oauth_tokens(enc: 前缀),重启不丢。
func (s *MCPService) newDBTokenStore(serverID int64) oauthTokenStore {
	return &dbTokenStore{svc: s, serverID: serverID}
}

type dbTokenStore struct {
	svc      *MCPService
	serverID int64
}

// loadRow 读 server 行(带锁)。
func (t *dbTokenStore) loadRow() (*mcpdomain.MCPServer, error) {
	t.svc.mu.RLock()
	repo := t.svc.serverRepo
	t.svc.mu.RUnlock()
	if repo == nil {
		return nil, errors.New("MCP 配置仓储未装配")
	}
	row, err := repo.GetByID(t.serverID)
	if err != nil || row == nil {
		return nil, fmt.Errorf("服务不存在(id=%d)", t.serverID)
	}
	return row, nil
}

func (t *dbTokenStore) GetToken() (*Token, error) {
	row, err := t.loadRow()
	if err != nil {
		return nil, err
	}
	if row.OAuthTokens == "" {
		return nil, errors.New("no token available")
	}
	plain, err := decryptSecret(row.OAuthTokens)
	if err != nil {
		return nil, fmt.Errorf("token 解密失败: %w", err)
	}
	var tok Token
	if err := json.Unmarshal([]byte(plain), &tok); err != nil {
		return nil, fmt.Errorf("token 数据损坏: %w", err)
	}
	return &tok, nil
}

func (t *dbTokenStore) SaveToken(tok *Token) error {
	row, err := t.loadRow()
	if err != nil {
		return err
	}
	b, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("token 序列化失败: %w", err)
	}
	cipher, err := encryptSecret(string(b))
	if err != nil {
		return err
	}
	t.svc.mu.RLock()
	repo := t.svc.serverRepo
	t.svc.mu.RUnlock()
	row.OAuthTokens = cipher
	return repo.Update(row)
}

// BeginOAuth 发起 OAuth 授权:
// 元数据发现(RFC 8414)→ (client_id 为空时)动态客户端注册(RFC 7591)→
// 生成授权 URL(含 PKCE)。state/verifier 挂起内存,回调时校验消费(CSRF 防护)。
func (s *MCPService) BeginOAuth(ctx context.Context, id int64) (*OAuthBeginResult, error) {
	s.mu.RLock()
	repo := s.serverRepo
	s.mu.RUnlock()
	if repo == nil {
		return nil, errors.New("MCP 配置仓储未装配")
	}
	row, err := repo.GetByID(id)
	if err != nil || row == nil {
		return nil, errors.New("服务不存在")
	}
	if row.AuthType != mcpdomain.AuthTypeOAuth {
		return nil, errors.New("该服务不是 OAuth 鉴权类型")
	}

	ep, err := discoverOAuthEndpoints(ctx, row.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("获取授权地址失败(服务不可达或不是 OAuth 服务): %w", err)
	}

	// 未填 client_id → 尝试动态客户端注册(RFC 7591),注册结果持久化
	if row.OAuthClientID == "" {
		if ep.RegistrationEndpoint == "" {
			return nil, errors.New("该服务不支持动态客户端注册,请在服务商后台创建客户端后填入 Client ID")
		}
		reg, err := registerOAuthClient(ctx, ep.RegistrationEndpoint, s.oauthRedirectURI())
		if err != nil {
			return nil, fmt.Errorf("该服务不支持动态客户端注册,请在服务商后台创建客户端后填入 Client ID(%v)", err)
		}
		row.OAuthClientID = reg.ClientID
		cipherSecret, err := encryptSecret(reg.ClientSecret)
		if err != nil {
			return nil, err
		}
		row.OAuthClientSecret = cipherSecret
		if err := repo.Update(row); err != nil {
			return nil, fmt.Errorf("保存注册结果失败: %w", err)
		}
	}

	state, err := generateRandomB64(32)
	if err != nil {
		return nil, err
	}
	verifier, err := generateRandomB64(64)
	if err != nil {
		return nil, err
	}

	authURL, err := buildAuthorizationURL(ep.AuthorizationEndpoint, url.Values{
		"response_type":         {"code"},
		"client_id":             {row.OAuthClientID},
		"redirect_uri":          {s.oauthRedirectURI()},
		"state":                 {state},
		"code_challenge":        {pkceS256Challenge(verifier)},
		"code_challenge_method": {"S256"},
		"scope":                 {row.OAuthScopes},
	})
	if err != nil {
		return nil, err
	}

	s.pendingMu.Lock()
	s.pendingOAuth[state] = &pendingOAuth{
		ServerID: id, Verifier: verifier,
		ExpiresAt: time.Now().Add(oauthPendingTTL),
	}
	s.pendingMu.Unlock()
	return &OAuthBeginResult{AuthorizationURL: authURL, State: state}, nil
}

// HandleOAuthCallback OAuth 服务商重定向回调:校验 state → 换 token(dbTokenStore 落库)。
// state 一次性消费;无效/过期/重放均拒绝。
func (s *MCPService) HandleOAuthCallback(ctx context.Context, code, state string) error {
	s.pendingMu.RLock()
	p, ok := s.pendingOAuth[state]
	s.pendingMu.RUnlock()
	if !ok || time.Now().After(p.ExpiresAt) {
		return errors.New("无效或已过期的授权回调(state 校验失败)")
	}

	row, err := (&dbTokenStore{svc: s, serverID: p.ServerID}).loadRow()
	if err != nil {
		return err
	}
	ep, err := discoverOAuthEndpoints(ctx, row.BaseURL)
	if err != nil {
		return fmt.Errorf("授权码换取令牌失败: %w", err)
	}
	clientSecret, _ := decryptSecret(row.OAuthClientSecret)
	tok, err := exchangeOAuthCode(ctx, ep.TokenEndpoint, code, p.Verifier,
		row.OAuthClientID, clientSecret, s.oauthRedirectURI())
	if err != nil {
		return fmt.Errorf("授权码换取令牌失败: %w", err)
	}
	if err := s.newDBTokenStore(p.ServerID).SaveToken(tok); err != nil {
		return fmt.Errorf("保存令牌失败: %w", err)
	}
	s.pendingMu.Lock()
	delete(s.pendingOAuth, state)
	s.pendingMu.Unlock()
	return nil
}

// refreshTokenIfExpired 连接前调用:token 过期且有 refresh_token 时自动刷新(结果落库)。
func (s *MCPService) refreshTokenIfExpired(ctx context.Context, serverID int64) (*Token, error) {
	store := s.newDBTokenStore(serverID)
	tok, err := store.GetToken()
	if err != nil {
		return nil, err // 未授权/数据损坏
	}
	if !tok.IsExpired() || tok.RefreshToken == "" {
		return tok, nil
	}
	row, err := store.(*dbTokenStore).loadRow()
	if err != nil {
		return nil, err
	}
	clientSecret, _ := decryptSecret(row.OAuthClientSecret)
	ep, err := discoverOAuthEndpoints(ctx, row.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("刷新令牌失败(服务不可达): %w", err)
	}
	tok, err = refreshOAuthToken(ctx, ep.TokenEndpoint, tok.RefreshToken, row.OAuthClientID, clientSecret)
	if err != nil {
		return nil, err
	}
	if err := store.SaveToken(tok); err != nil {
		return nil, fmt.Errorf("保存刷新令牌失败: %w", err)
	}
	return tok, nil
}

// ---- OAuth 原语(自实现,替代 mark3labs clienttransport) ----

// generateRandomB64 生成 n 字节随机数的 base64url 串(state/verifier)。
func generateRandomB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// pkceS256Challenge PKCE code_challenge:S256(base64url(sha256(verifier)))。
func pkceS256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// discoverOAuthEndpoints RFC 8414 元数据发现:
// {base}/.well-known/oauth-authorization-server(带 path 时叠加 path 变体)。
func discoverOAuthEndpoints(ctx context.Context, baseURL string) (*oauthEndpoints, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("地址解析失败: %w", err)
	}
	base := u.Scheme + "://" + u.Host
	candidates := []string{
		base + "/.well-known/oauth-authorization-server" + strings.TrimSuffix(u.Path, "/"),
		base + "/.well-known/oauth-authorization-server",
	}
	client := &http.Client{Timeout: MCPToolTimeout}
	for _, meta := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, meta, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		ep := &oauthEndpoints{}
		func() {
			defer resp.Body.Close()
			body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			if err == nil {
				_ = json.Unmarshal(body, ep)
			}
		}()
		if resp.StatusCode == http.StatusOK && ep.AuthorizationEndpoint != "" {
			return ep, nil
		}
	}
	return nil, errors.New("未发现 OAuth 元数据")
}

// oauthClientCredentials 动态客户端注册结果(RFC 7591,只取用到的字段)。
type oauthClientCredentials struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// registerOAuthClient 动态客户端注册(go-sdk sdkoauthex.RegisterClient,RFC 7591)。
func registerOAuthClient(ctx context.Context, registrationEndpoint, redirectURI string) (*oauthClientCredentials, error) {
	meta := &sdkoauthex.ClientRegistrationMetadata{
		RedirectURIs:            []string{redirectURI},
		TokenEndpointAuthMethod: "client_secret_post",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              "omnibot",
	}
	reg, err := sdkoauthex.RegisterClient(ctx, registrationEndpoint, meta, &http.Client{Timeout: MCPToolTimeout})
	if err != nil {
		return nil, err
	}
	return &oauthClientCredentials{ClientID: reg.ClientID, ClientSecret: reg.ClientSecret}, nil
}

// buildAuthorizationURL 拼授权 URL。
func buildAuthorizationURL(endpoint string, q url.Values) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("授权端点解析失败: %w", err)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// exchangeOAuthCode 授权码换 token(标准 OAuth2 POST,PKCE)。
func exchangeOAuthCode(ctx context.Context, tokenEndpoint, code, verifier, clientID, clientSecret, redirectURI string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	return oauthTokenRequest(ctx, tokenEndpoint, form)
}

// refreshOAuthToken 刷新令牌。
func refreshOAuthToken(ctx context.Context, tokenEndpoint, refreshToken, clientID, clientSecret string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	return oauthTokenRequest(ctx, tokenEndpoint, form)
}

// oauthTokenRequest 发 token 请求并解析(错误响应透出服务商描述)。
func oauthTokenRequest(ctx context.Context, tokenEndpoint string, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: MCPToolTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok Token
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("令牌响应解析失败: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, errors.New("令牌响应缺少 access_token")
	}
	if tok.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return &tok, nil
}

// splitScopes 逗号分隔 → scope 列表。
func splitScopes(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
