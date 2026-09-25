package mcp

import (
	"context"
	"fmt"
	"strings"

	mcpdomain "omnibot/internal/domain/mcp"
	"omnibot/internal/pkg/crypto"
)

// MCPServerRepository MCP server 配置持久化窄接口(service 层声明,repository 层实现)。
type MCPServerRepository interface {
	Create(server *mcpdomain.MCPServer) error
	Update(server *mcpdomain.MCPServer) error
	Delete(id int64) error
	GetByID(id int64) (*mcpdomain.MCPServer, error)
	GetByName(name string) (*mcpdomain.MCPServer, error)
	List() ([]*mcpdomain.MCPServer, error)
	Count() (int64, error)
}

// SyncResult 单次同步结果(Err 空=成功;失败以字段表达,不作为调用错误)。
type SyncResult struct {
	ServerName string `json:"server_name"`
	ToolCount  int    `json:"tool_count"`
	Err        string `json:"err,omitempty"`
}

// ---- 密钥加密助手(密文带 enc: 前缀;无前缀视为历史明文,兼容 yaml seed 前 ||调试) ----

const secretPrefix = "enc:"

func encryptSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	cipher, err := crypto.Encrypt(plain)
	if err != nil {
		return "", fmt.Errorf("skill: encrypt secret: %w", err)
	}
	return secretPrefix + cipher, nil
}

func decryptSecret(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, secretPrefix) {
		return stored, nil // 历史明文(不应出现,容错直接用)
	}
	return crypto.Decrypt(strings.TrimPrefix(stored, secretPrefix))
}

// SetMCPServerRepository 注入 server 配置仓储(M3 在线配置)。
func (s *MCPService) SetMCPServerRepository(repo MCPServerRepository) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serverRepo = repo
}

// validateServerInput 增改共用校验。
func validateServerInput(name, baseURL string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("名称不能为空")
	}
	if baseURL == "" {
		return fmt.Errorf("服务地址不能为空")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return fmt.Errorf("服务地址必须是 http(s) 链接")
	}
	return nil
}

// MCPServerInput 新增/更新入参(api_key/client_secret 空 = 更新时保留原值)。
type MCPServerInput struct {
	Name              string
	Description       string // 能力概述(选填)
	BaseURL           string
	APIKey            string
	AuthType          string // none/bearer/oauth,空 = bearer
	Transport         string // streamable(空同)/sse
	OAuthClientID     string
	OAuthClientSecret string
	OAuthScopes       string
	Enabled           bool
	Shared            bool // true = 共享服务(所有用户可调用);默认私有(归属创建者)
}

// normalizeAuthType 鉴权方式校验与归一。
func normalizeAuthType(t string) (string, error) {
	switch t {
	case "":
		return mcpdomain.AuthTypeBearer, nil
	case mcpdomain.AuthTypeNone, mcpdomain.AuthTypeBearer, mcpdomain.AuthTypeOAuth, mcpdomain.AuthTypeQuery:
		return t, nil
	default:
		return "", fmt.Errorf("不支持的鉴权方式 %q", t)
	}
}

// normalizeTransport 传输协议校验:空值保留(= streamable + 同步失败自动回退 SSE),
// 只有用户显式选择 streamable 才固定协议、放弃回退。
func normalizeTransport(t string) (string, error) {
	switch t {
	case "", mcpdomain.TransportStreamable, mcpdomain.TransportSSE:
		return t, nil
	default:
		return "", fmt.Errorf("不支持的传输协议 %q(可选 streamable / sse)", t)
	}
}

// AddServer 新增 MCP server:加密落库(归属创建者,NULL=共享)→ enabled 则立即同步。
func (s *MCPService) AddServer(in MCPServerInput, userID int64) (*mcpdomain.ServerView, error) {
	name := strings.TrimSpace(in.Name)
	if err := validateServerInput(name, in.BaseURL); err != nil {
		return nil, err
	}
	authType, err := normalizeAuthType(in.AuthType)
	if err != nil {
		return nil, err
	}
	transport, err := normalizeTransport(in.Transport)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	repo := s.serverRepo
	s.mu.RUnlock()
	if repo == nil {
		return nil, fmt.Errorf("MCP 配置仓储未装配")
	}
	if existing, _ := repo.GetByName(name); existing != nil {
		return nil, fmt.Errorf("已存在同名服务 %q", name)
	}
	cipher, err := encryptSecret(in.APIKey)
	if err != nil {
		return nil, err
	}
	cipherSecret, err := encryptSecret(in.OAuthClientSecret)
	if err != nil {
		return nil, err
	}
	var owner *int64
	if !in.Shared {
		owner = &userID
	}
	row := &mcpdomain.MCPServer{
		Name: name, BaseURL: in.BaseURL, APIKey: cipher, Enabled: in.Enabled,
		Description:       strings.TrimSpace(in.Description),
		AuthType:          authType,
		Transport:         transport,
		UserID:            owner,
		OAuthClientID:     strings.TrimSpace(in.OAuthClientID),
		OAuthClientSecret: cipherSecret,
		OAuthScopes:       strings.TrimSpace(in.OAuthScopes),
	}
	if err := repo.Create(row); err != nil {
		return nil, fmt.Errorf("保存服务失败: %w", err)
	}
	if in.Enabled {
		s.syncServerRow(row) // 结果只进日志;用户可手动重试
	}
	return s.serverToView(row)
}

// UpdateServer 更新配置并按需重新同步(仅归属人可改;apiKey/client_secret 空 = 保留原值)。
func (s *MCPService) UpdateServer(id int64, in MCPServerInput, userID int64) (*mcpdomain.ServerView, error) {
	name := strings.TrimSpace(in.Name)
	if err := validateServerInput(name, in.BaseURL); err != nil {
		return nil, err
	}
	authType, err := normalizeAuthType(in.AuthType)
	if err != nil {
		return nil, err
	}
	transport, err := normalizeTransport(in.Transport)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	repo := s.serverRepo
	s.mu.RUnlock()
	if repo == nil {
		return nil, fmt.Errorf("MCP 配置仓储未装配")
	}
	row, err := repo.GetByID(id)
	if err != nil || row == nil {
		return nil, fmt.Errorf("服务不存在")
	}
	if row.UserID != nil && *row.UserID != userID {
		return nil, fmt.Errorf("服务不存在") // 私有服务不外泄存在性
	}
	if other, _ := repo.GetByName(name); other != nil && other.ID != id {
		return nil, fmt.Errorf("已存在同名服务 %q", name)
	}

	wasEnabled := row.Enabled
	row.Name = name
	row.BaseURL = in.BaseURL
	row.Enabled = in.Enabled
	row.Description = strings.TrimSpace(in.Description)
	row.AuthType = authType
	row.Transport = transport
	if in.APIKey != "" {
		cipher, err := encryptSecret(in.APIKey)
		if err != nil {
			return nil, err
		}
		row.APIKey = cipher
	}
	if in.OAuthClientID != "" {
		row.OAuthClientID = strings.TrimSpace(in.OAuthClientID)
	}
	if in.OAuthClientSecret != "" {
		cipher, err := encryptSecret(in.OAuthClientSecret)
		if err != nil {
			return nil, err
		}
		row.OAuthClientSecret = cipher
	}
	row.OAuthScopes = strings.TrimSpace(in.OAuthScopes)
	if err := repo.Update(row); err != nil {
		return nil, fmt.Errorf("保存服务失败: %w", err)
	}

	// 开关变化:停用 → 目录即时失效(工具不可调用、不参与匹配);开启 → 重新同步
	if wasEnabled && !row.Enabled {
		s.catalog.remove(row.ID)
	} else if row.Enabled {
		s.syncServerRow(row)
	}
	return s.serverToView(row)
}

// DeleteServer 删除 server:目录即时失效(内存缓存,无库表残留)。
func (s *MCPService) DeleteServer(id int64) error {
	s.mu.RLock()
	repo := s.serverRepo
	s.mu.RUnlock()
	if repo == nil {
		return fmt.Errorf("MCP 配置仓储未装配")
	}
	row, err := repo.GetByID(id)
	if err != nil || row == nil {
		return fmt.Errorf("服务不存在")
	}
	if err := repo.Delete(id); err != nil {
		return fmt.Errorf("删除服务失败: %w", err)
	}
	// 目录随连接器删除即时失效(内存缓存,无库表残留)
	s.catalog.remove(row.ID)
	return nil
}

// SyncServer 手动同步单个 server(共享可同步;私有仅归属人)。失败以 SyncResult.Err 表达。
func (s *MCPService) SyncServer(id int64, userID int64) (*SyncResult, error) {
	s.mu.RLock()
	repo := s.serverRepo
	s.mu.RUnlock()
	if repo == nil {
		return nil, fmt.Errorf("MCP 配置仓储未装配")
	}
	row, err := repo.GetByID(id)
	if err != nil || row == nil {
		return nil, fmt.Errorf("服务不存在")
	}
	if row.UserID != nil && *row.UserID != userID {
		return nil, fmt.Errorf("服务不存在")
	}
	if !row.Enabled {
		return nil, fmt.Errorf("服务已停用,请先开启")
	}
	return s.syncServerRow(row), nil
}

// ListServers 掩码视图列表(共享 + 本人私有,按 id 升序)。
func (s *MCPService) ListServers(userID int64) ([]mcpdomain.ServerView, error) {
	s.mu.RLock()
	repo := s.serverRepo
	s.mu.RUnlock()
	if repo == nil {
		return nil, fmt.Errorf("MCP 配置仓储未装配")
	}
	rows, err := repo.List()
	if err != nil {
		return nil, err
	}
	filtered := make([]*mcpdomain.MCPServer, 0, len(rows))
	for _, r := range rows {
		if r.UserID == nil || *r.UserID == userID {
			filtered = append(filtered, r)
		}
	}
	rows = filtered
	views := make([]mcpdomain.ServerView, 0, len(rows))
	for _, row := range rows {
		view, err := s.serverToView(row)
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

// serverToView 行 → 掩码视图(工具数直接统计该 server 的 mcp 技能行,缺省 -1=从未同步成功)。
func (s *MCPService) serverToView(row *mcpdomain.MCPServer) (*mcpdomain.ServerView, error) {
	view := &mcpdomain.ServerView{
		ID:          row.ID,
		Name:        row.Name,
		BaseURL:     row.BaseURL,
		Description: row.Description,
		Enabled:     row.Enabled,
		HasAPIKey:   row.APIKey != "",
		AuthType:   row.AuthType,
		Transport:  row.Transport,
		Authorized: row.Authorized(),
		ToolCount:  -1,
	}
	if s.catalog != nil {
		s.catalog.mu.RLock()
		if tools, ok := s.catalog.byServer[row.ID]; ok {
			view.ToolCount = len(tools) // 目录现实;未同步过仍为 -1
			view.Tools = make([]mcpdomain.MCPToolCard, 0, len(tools))
			for _, t := range tools {
				view.Tools = append(view.Tools, mcpdomain.MCPToolCard{Name: t.Name, Description: t.Description})
			}
		}
		s.catalog.mu.RUnlock()
	}
	return view, nil
}

// SeedServersFromConfig 首次启动 seed:仅当库内无 server 时导入 yaml 配置(加密落库)。
// 返回导入数;库非空时为 0(DB 是唯一事实源)。
func (s *MCPService) SeedServersFromConfig(specs []MCPServerSpec) (int, error) {
	s.mu.RLock()
	repo := s.serverRepo
	s.mu.RUnlock()
	if repo == nil || len(specs) == 0 {
		return 0, nil
	}
	cnt, err := repo.Count()
	if err != nil {
		return 0, err
	}
	if cnt > 0 {
		return 0, nil
	}
	imported := 0
	for _, spec := range specs {
		cipher, err := encryptSecret(spec.APIKey)
		if err != nil {
			return imported, err
		}
		if err := repo.Create(&mcpdomain.MCPServer{
			Name: spec.Name, BaseURL: spec.BaseURL, APIKey: cipher, Enabled: spec.Enabled,
		}); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// SyncAllServers 启动同步:全部 enabled server 逐个同步(工具目录入内存缓存)。
// 单个失败不阻塞(结果进日志)。
func (s *MCPService) SyncAllServers(ctx context.Context) error {
	s.mu.RLock()
	repo := s.serverRepo
	s.mu.RUnlock()
	if repo == nil {
		return nil
	}
	rows, err := repo.List()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		s.syncServerRow(row)
	}
	return nil
}

// syncServerRow 同步单个 server 行(解密 key → 连接 → 发现 → 落库/注册执行体)。
func (s *MCPService) syncServerRow(row *mcpdomain.MCPServer) *SyncResult {
	res := &SyncResult{ServerName: row.Name}
	s.mu.RLock()
	factory := s.mcpFactory
	s.mu.RUnlock()
	if factory == nil {
		res.Err = "MCP 客户端工厂未装配"
		fmt.Printf("[skill] mcp server %q: %s\n", row.Name, res.Err)
		return res
	}

	spec := MCPServerSpec{Name: row.Name, BaseURL: row.BaseURL, Enabled: true, AuthType: row.AuthType, Transport: row.Transport}
	switch row.AuthType {
	case mcpdomain.AuthTypeOAuth:
		// 未授权 → 不连接,提示先走授权流程
		if row.OAuthTokens == "" {
			res.Err = "尚未完成 OAuth 授权,请先点击「授权」"
			fmt.Printf("[skill] mcp server %q: %s\n", row.Name, res.Err)
			return res
		}
		// token 过期则先刷新(失败如实上报,不静默用旧 token);
		// go-sdk 迁移后 OAuth 客户端 == Bearer 客户端(access token 作为 Bearer 注入)
		tok, err := s.refreshTokenIfExpired(context.Background(), row.ID)
		if err != nil {
			res.Err = fmt.Sprintf("OAuth 令牌刷新失败: %v", err)
			fmt.Printf("[skill] mcp server %q: %s\n", row.Name, res.Err)
			return res
		}
		spec.APIKey = tok.AccessToken
	default: // bearer / none
		apiKey, err := decryptSecret(row.APIKey)
		if err != nil {
			res.Err = fmt.Sprintf("密钥解密失败: %v", err)
			fmt.Printf("[skill] mcp server %q: %s\n", row.Name, res.Err)
			return res
		}
		spec.APIKey = apiKey
	}

	tools, resErr := listToolsVia(factory, spec)
	if resErr == "" {
		res.ToolCount = s.replaceServerCatalog(row, tools)
	}

	// 自动回退(Transport 空值语义):主选 streamable 连接/初始化失败 → 依次尝试
	// SSE / query 鉴权(key=URL 参数,高德惯例) / SSE+query,直到一组成功。
	// 只在同步时发生(启动/保存/手动),不影响工具调用路径。
	if resErr != "" && row.Transport == "" && row.AuthType != mcpdomain.AuthTypeOAuth {
		apiKey := spec.APIKey
		trials := []MCPServerSpec{
			func() MCPServerSpec { sp := spec; sp.Transport = mcpdomain.TransportSSE; return sp }(),
		}
		if apiKey != "" && row.AuthType == mcpdomain.AuthTypeBearer {
			q := func(tp string) MCPServerSpec {
				sp := MCPServerSpec{Name: spec.Name, BaseURL: spec.BaseURL, Enabled: true,
					AuthType: mcpdomain.AuthTypeQuery, Transport: tp, APIKey: apiKey}
				return sp
			}
			trials = append(trials, q(mcpdomain.TransportStreamable), q(mcpdomain.TransportSSE))
		}
		for _, trial := range trials {
			fmt.Printf("[skill] mcp server %q 主选失败(%s),回退重试: transport=%s auth=%s\n",
				row.Name, resErr, trial.Transport, trial.AuthType)
			tools, trialErr := listToolsVia(factory, trial)
			if trialErr != "" {
				resErr = trialErr
				continue
			}
			res.ToolCount = s.replaceServerCatalog(row, tools)
			resErr = ""
			// 回退组合生效:持久化到 server 行,下次同步直连,不再重复探测
			if trial.AuthType != row.AuthType || trial.Transport != row.Transport {
				row.AuthType = trial.AuthType
				row.Transport = trial.Transport
				if repo := s.serverRepo; repo != nil {
					if err := repo.Update(row); err != nil {
						fmt.Printf("[skill] mcp server %q 回退组合持久化失败(下次仍会探测): %v\n", row.Name, err)
					} else {
						fmt.Printf("[skill] mcp server %q 生效组合已持久化: transport=%s auth=%s\n",
							row.Name, trial.Transport, trial.AuthType)
					}
				}
			}
			break
		}
	}

	if resErr != "" {
		res.Err = resErr
		fmt.Printf("[skill] mcp server %q 同步失败: %s\n", row.Name, resErr)
	}
	return res
}

// connectMCPClient 经工厂建连(go-sdk Connect 内聚握手),错误文案逐步包裹。
// 原 Start→Initialize 两步已由 SDK 的 Connect 一步替代。
func connectMCPClient(factory MCPClientFactory, spec MCPServerSpec) (MCPClient, string) {
	connectCtx, cancel := context.WithTimeout(context.Background(), MCPToolTimeout)
	defer cancel()
	mcpClient, err := factory(connectCtx, spec)
	if err != nil {
		return nil, fmt.Sprintf("连接失败: %v", err)
	}
	return mcpClient, ""
}

// listToolsVia 建连→拉工具列表→关闭,回退探测用(整链路带超时,资源确定释放)。
func listToolsVia(factory MCPClientFactory, spec MCPServerSpec) ([]mcpdomain.MCPRemoteTool, string) {
	mcpClient, cerr := connectMCPClient(factory, spec)
	if cerr != "" {
		return nil, cerr
	}
	defer func() { _ = mcpClient.Close() }()
	listCtx, cancel := context.WithTimeout(context.Background(), MCPToolTimeout)
	defer cancel()
	tools, err := mcpClient.ListTools(listCtx)
	if err != nil {
		return nil, fmt.Sprintf("获取工具列表失败: %v", err)
	}
	return tools, ""
}

// derefOrZero nil 安全取值。
func derefOrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
