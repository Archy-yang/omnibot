package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	clienttransport "github.com/mark3labs/mcp-go/client/transport"

	skilldomain "omnibot/internal/domain/skill"
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
	Handler   *clienttransport.OAuthHandler
	ExpiresAt time.Time
}

// SetOAuthRedirectBase 设置 OAuth 回调基址(如 https://bot.example.com)。
// 完整 redirect_uri = <base>/api/v1/mcp/oauth/callback,须与服务商登记一致。
func (s *SkillService) SetOAuthRedirectBase(base string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.oauthRedirectBase = strings.TrimRight(base, "/")
}

// oauthRedirectURI 完整回调地址。
func (s *SkillService) oauthRedirectURI() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.oauthRedirectBase + oauthCallbackPath
}

// newDBTokenStore 构造绑定指定 server 的 DB token 存储:
// token JSON 整体 AES 加密落 mcp_servers.oauth_tokens(enc: 前缀)。
// 经 OAuthHandler 使用时,授权换新与 refresh 自动持久化,重启不丢。
func (s *SkillService) newDBTokenStore(serverID int64) clienttransport.TokenStore {
	return &dbTokenStore{svc: s, serverID: serverID}
}

type dbTokenStore struct {
	svc      *SkillService
	serverID int64
}

// loadRow 读 server 行(带锁)。
func (t *dbTokenStore) loadRow() (*skilldomain.MCPServer, error) {
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

func (t *dbTokenStore) GetToken() (*clienttransport.Token, error) {
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
	var tok clienttransport.Token
	if err := json.Unmarshal([]byte(plain), &tok); err != nil {
		return nil, fmt.Errorf("token 数据损坏: %w", err)
	}
	return &tok, nil
}

func (t *dbTokenStore) SaveToken(tok *clienttransport.Token) error {
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
// 元数据发现 → (client_id 为空时)动态客户端注册 → 生成授权 URL(含 PKCE)。
// state/verifier 挂起内存,回调时校验消费(CSRF 防护)。
func (s *SkillService) BeginOAuth(ctx context.Context, id int64) (*OAuthBeginResult, error) {
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
	if row.AuthType != skilldomain.AuthTypeOAuth {
		return nil, errors.New("该服务不是 OAuth 鉴权类型")
	}

	clientSecret, err := decryptSecret(row.OAuthClientSecret)
	if err != nil {
		return nil, fmt.Errorf("客户端密钥解密失败: %w", err)
	}
	handler := clienttransport.NewOAuthHandler(clienttransport.OAuthConfig{
		ClientID:     row.OAuthClientID,
		ClientSecret: clientSecret,
		RedirectURI:  s.oauthRedirectURI(),
		Scopes:       splitScopes(row.OAuthScopes),
		TokenStore:   s.newDBTokenStore(id),
		PKCEEnabled:  true,
	})
	handler.SetBaseURL(row.BaseURL)

	// 未填 client_id → 尝试动态客户端注册(RFC 7591),注册结果持久化
	if row.OAuthClientID == "" {
		if err := handler.RegisterClient(ctx, "omnibot"); err != nil {
			return nil, fmt.Errorf("该服务不支持动态客户端注册,请在服务商后台创建客户端后填入 Client ID(%v)", err)
		}
		row.OAuthClientID = handler.GetClientID()
		cipherSecret, err := encryptSecret(handler.GetClientSecret())
		if err != nil {
			return nil, err
		}
		row.OAuthClientSecret = cipherSecret
		if err := repo.Update(row); err != nil {
			return nil, fmt.Errorf("保存注册结果失败: %w", err)
		}
	}

	state, err := clienttransport.GenerateState()
	if err != nil {
		return nil, err
	}
	verifier, err := clienttransport.GenerateCodeVerifier()
	if err != nil {
		return nil, err
	}
	authURL, err := handler.GetAuthorizationURL(ctx, state, clienttransport.GenerateCodeChallenge(verifier))
	if err != nil {
		return nil, fmt.Errorf("获取授权地址失败(服务不可达或不是 OAuth 服务): %w", err)
	}

	s.pendingMu.Lock()
	s.pendingOAuth[state] = &pendingOAuth{
		ServerID: id, Verifier: verifier, Handler: handler,
		ExpiresAt: time.Now().Add(oauthPendingTTL),
	}
	s.pendingMu.Unlock()
	return &OAuthBeginResult{AuthorizationURL: authURL, State: state}, nil
}

// HandleOAuthCallback OAuth 服务商重定向回调:校验 state → 换 token(dbTokenStore 落库)。
// state 一次性消费;无效/过期/重放均拒绝。
func (s *SkillService) HandleOAuthCallback(ctx context.Context, code, state string) error {
	s.pendingMu.RLock()
	p, ok := s.pendingOAuth[state]
	s.pendingMu.RUnlock()
	if !ok || time.Now().After(p.ExpiresAt) {
		return errors.New("无效或已过期的授权回调(state 校验失败)")
	}
	if err := p.Handler.ProcessAuthorizationResponse(ctx, code, state, p.Verifier); err != nil {
		return fmt.Errorf("授权码换取令牌失败: %w", err)
	}
	s.pendingMu.Lock()
	delete(s.pendingOAuth, state)
	s.pendingMu.Unlock()
	return nil // token 已由 dbTokenStore 持久化
}

// refreshTokenIfExpired 连接前调用:token 过期且有 refresh_token 时自动刷新(结果落库)。
func (s *SkillService) refreshTokenIfExpired(ctx context.Context, serverID int64) (*clienttransport.Token, error) {
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
	handler := clienttransport.NewOAuthHandler(clienttransport.OAuthConfig{
		ClientID:     row.OAuthClientID,
		ClientSecret: clientSecret,
		RedirectURI:  s.oauthRedirectURI(),
		Scopes:       splitScopes(row.OAuthScopes),
		TokenStore:   store,
		PKCEEnabled:  true,
	})
	handler.SetBaseURL(row.BaseURL)
	return handler.RefreshToken(ctx, tok.RefreshToken)
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
