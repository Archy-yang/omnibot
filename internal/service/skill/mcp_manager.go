package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"omnibot/internal/pkg/crypto"
	skilldomain "omnibot/internal/domain/skill"
)

// MCPServerRepository MCP server 配置持久化窄接口(service 层声明,repository 层实现)。
type MCPServerRepository interface {
	Create(server *skilldomain.MCPServer) error
	Update(server *skilldomain.MCPServer) error
	Delete(id int64) error
	GetByID(id int64) (*skilldomain.MCPServer, error)
	GetByName(name string) (*skilldomain.MCPServer, error)
	List() ([]*skilldomain.MCPServer, error)
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
func (s *SkillService) SetMCPServerRepository(repo MCPServerRepository) {
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
	BaseURL           string
	APIKey            string
	AuthType          string // none/bearer/oauth,空 = bearer
	OAuthClientID     string
	OAuthClientSecret string
	OAuthScopes       string
	Enabled           bool
}

// normalizeAuthType 鉴权方式校验与归一。
func normalizeAuthType(t string) (string, error) {
	switch t {
	case "":
		return skilldomain.AuthTypeBearer, nil
	case skilldomain.AuthTypeNone, skilldomain.AuthTypeBearer, skilldomain.AuthTypeOAuth:
		return t, nil
	default:
		return "", fmt.Errorf("不支持的鉴权方式 %q", t)
	}
}

// AddServer 新增 MCP server:加密落库 → enabled 则立即同步(失败不回滚,可手动重试)。
func (s *SkillService) AddServer(in MCPServerInput) (*skilldomain.ServerView, error) {
	name := strings.TrimSpace(in.Name)
	if err := validateServerInput(name, in.BaseURL); err != nil {
		return nil, err
	}
	authType, err := normalizeAuthType(in.AuthType)
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
	row := &skilldomain.MCPServer{
		Name: name, BaseURL: in.BaseURL, APIKey: cipher, Enabled: in.Enabled,
		AuthType:          authType,
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

// UpdateServer 更新配置并按需重新同步(enabled 切换会装卸执行体;apiKey/client_secret 空 = 保留原值)。
func (s *SkillService) UpdateServer(id int64, in MCPServerInput) (*skilldomain.ServerView, error) {
	name := strings.TrimSpace(in.Name)
	if err := validateServerInput(name, in.BaseURL); err != nil {
		return nil, err
	}
	authType, err := normalizeAuthType(in.AuthType)
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
	if other, _ := repo.GetByName(name); other != nil && other.ID != id {
		return nil, fmt.Errorf("已存在同名服务 %q", name)
	}

	wasEnabled := row.Enabled
	row.Name = name
	row.BaseURL = in.BaseURL
	row.Enabled = in.Enabled
	row.AuthType = authType
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

	// 开关变化 → 重新装卸
	if wasEnabled && !row.Enabled {
		s.dropServerRuntime(row.Name) // 停用:移除执行体,技能隐藏
	} else if row.Enabled {
		s.syncServerRow(row)
	}
	return s.serverToView(row)
}

// DeleteServer 删除 server 并级联删除其技能行、移除执行体。
func (s *SkillService) DeleteServer(id int64) error {
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
	s.dropServerRuntime(row.Name)
	if s.skillRepo() != nil {
		if _, err := s.skillRepo().DeleteMCPSkillsByServer(row.Name); err != nil {
			return fmt.Errorf("级联删除技能失败: %w", err)
		}
	}
	return nil
}

// SyncServer 手动同步单个 server。失败以 SyncResult.Err 表达(调用无错)。
func (s *SkillService) SyncServer(id int64) (*SyncResult, error) {
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
	if !row.Enabled {
		return nil, fmt.Errorf("服务已停用,请先开启")
	}
	return s.syncServerRow(row), nil
}

// ListServers 掩码视图列表(按 id 升序)。
func (s *SkillService) ListServers() ([]skilldomain.ServerView, error) {
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
	views := make([]skilldomain.ServerView, 0, len(rows))
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
func (s *SkillService) serverToView(row *skilldomain.MCPServer) (*skilldomain.ServerView, error) {
	view := &skilldomain.ServerView{
		ID:         row.ID,
		Name:       row.Name,
		BaseURL:    row.BaseURL,
		Enabled:    row.Enabled,
		HasAPIKey:  row.APIKey != "",
		AuthType:   row.AuthType,
		Authorized: row.Authorized(),
		ToolCount:  -1,
	}
	if skillRepo := s.skillRepo(); skillRepo != nil {
		if rows, err := skillRepo.List(); err == nil {
			n := 0
			for _, r := range rows {
				if r.Source == skilldomain.SourceMCP && r.MCPServer == row.Name {
					n++
				}
			}
			view.ToolCount = n
		}
	}
	return view, nil
}

// SeedServersFromConfig 首次启动 seed:仅当库内无 server 时导入 yaml 配置(加密落库)。
// 返回导入数;库非空时为 0(DB 是唯一事实源)。
func (s *SkillService) SeedServersFromConfig(specs []MCPServerSpec) (int, error) {
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
		if err := repo.Create(&skilldomain.MCPServer{
			Name: spec.Name, BaseURL: spec.BaseURL, APIKey: cipher, Enabled: spec.Enabled,
		}); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

// SyncAllServers 启动同步:从 DB 读全部 enabled server(解密 key)逐个同步。
// 单个失败不阻塞(结果进日志);全部完成后由装配点 ApplyTo。
func (s *SkillService) SyncAllServers(ctx context.Context) error {
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

// dropServerRuntime 移除某 server 全部技能的执行体(技能隐藏)。
func (s *SkillService) dropServerRuntime(serverName string) {
	skillRepo := s.skillRepo()
	if skillRepo == nil {
		return
	}
	rows, err := skillRepo.List()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows {
		if r.Source == skilldomain.SourceMCP && r.MCPServer == serverName {
			delete(s.mcpExecutors, r.Name)
		}
	}
}

// syncServerRow 同步单个 server 行(解密 key → 连接 → 发现 → 落库/注册执行体)。
func (s *SkillService) syncServerRow(row *skilldomain.MCPServer) *SyncResult {
	res := &SyncResult{ServerName: row.Name}
	s.mu.RLock()
	factory := s.mcpFactory
	s.mu.RUnlock()
	if factory == nil {
		res.Err = "MCP 客户端工厂未装配"
		fmt.Printf("[skill] mcp server %q: %s\n", row.Name, res.Err)
		return res
	}

	spec := MCPServerSpec{Name: row.Name, BaseURL: row.BaseURL, Enabled: true, AuthType: row.AuthType}
	switch row.AuthType {
	case skilldomain.AuthTypeOAuth:
		// 未授权 → 不连接,提示先走授权流程
		if row.OAuthTokens == "" {
			res.Err = "尚未完成 OAuth 授权,请先点击「授权」"
			fmt.Printf("[skill] mcp server %q: %s\n", row.Name, res.Err)
			return res
		}
		// token 过期则先刷新(失败如实上报,不静默用旧 token)
		if _, err := s.refreshTokenIfExpired(context.Background(), row.ID); err != nil {
			res.Err = fmt.Sprintf("OAuth 令牌刷新失败: %v", err)
			fmt.Printf("[skill] mcp server %q: %s\n", row.Name, res.Err)
			return res
		}
		clientSecret, err := decryptSecret(row.OAuthClientSecret)
		if err != nil {
			res.Err = fmt.Sprintf("客户端密钥解密失败: %v", err)
			fmt.Printf("[skill] mcp server %q: %s\n", row.Name, res.Err)
			return res
		}
		spec.OAuthClientID = row.OAuthClientID
		spec.OAuthClientSecret = clientSecret
		spec.OAuthScopes = row.OAuthScopes
		spec.TokenStore = s.newDBTokenStore(row.ID)
	default: // bearer / none
		apiKey, err := decryptSecret(row.APIKey)
		if err != nil {
			res.Err = fmt.Sprintf("密钥解密失败: %v", err)
			fmt.Printf("[skill] mcp server %q: %s\n", row.Name, res.Err)
			return res
		}
		spec.APIKey = apiKey
	}

	mcpClient, err := factory(spec)
	if err != nil {
		res.Err = fmt.Sprintf("创建客户端失败: %v", err)
	} else if err := mcpClient.Start(context.Background()); err != nil {
		res.Err = fmt.Sprintf("连接失败: %v", err)
	}
	if res.Err == "" {
		initReq := mcpInitializeRequest()
		if _, err := mcpClient.Initialize(context.Background(), initReq); err != nil {
			res.Err = fmt.Sprintf("初始化失败: %v", err)
		}
	}
	if res.Err == "" {
		result, err := mcpClient.ListTools(context.Background(), mcpListToolsRequest())
		if err != nil {
			res.Err = fmt.Sprintf("获取工具列表失败: %v", err)
		} else {
			res.ToolCount = s.ingestServerTools(row.Name, mcpClient, result.Tools)
		}
	}
	if res.Err != "" {
		fmt.Printf("[skill] mcp server %q 同步失败: %s\n", row.Name, res.Err)
	}
	return res
}

// mcpInitializeRequest / mcpListToolsRequest 协议请求构造(共享)。
func mcpInitializeRequest() mcp.InitializeRequest {
	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "omnibot", Version: "1.0"}
	return req
}

func mcpListToolsRequest() mcp.ListToolsRequest {
	return mcp.ListToolsRequest{}
}

// ingestServerTools 把发现的远端工具落库(默认停用,重名跳过)+ 注册执行体。
// 返回成功入库的工具数。共享:SyncAllServers(syncServerRow)与 SyncMCPServers(装配旧路径)。
func (s *SkillService) ingestServerTools(serverName string, mcpClient MCPClient, tools []mcp.Tool) int {
	skillRepo := s.skillRepo()
	count := 0
	for _, tool := range tools {
		s.mu.RLock()
		_, conflict := s.builders[tool.Name]
		s.mu.RUnlock()
		if conflict {
			fmt.Printf("[skill] mcp tool %q (server %s) conflicts with builtin, skipped\n", tool.Name, serverName)
			continue
		}

		schemaJSON, _ := json.Marshal(tool.InputSchema)
		def := skilldomain.MCPToolDef{
			Name:         tool.Name,
			DisplayName:  tool.Name,
			Description:  tool.Description,
			MCPServer:    serverName,
			ParamsSchema: string(schemaJSON),
			MainVisible:  true, // 远端技能默认主 Agent 也可用(抓取类限制只针对内置)
			Enabled:      false,
		}
		if skillRepo != nil {
			if err := skillRepo.UpsertMCPTool(def); err != nil {
				fmt.Printf("[skill] mcp tool %q upsert failed: %v\n", tool.Name, err)
				continue
			}
		}
		s.registerMCPExecutor(tool.Name, makeMCPToolExecutor(mcpClient, tool.Name))
		count++
	}
	return count
}
