package mcp

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	mcp "github.com/mark3labs/mcp-go/mcp"

	mcpdomain "omnibot/internal/domain/mcp"
	agentpkg "omnibot/internal/service/agent"
	memoryservice "omnibot/internal/service/memory"
)

// B2:MCP 工具目录(内存缓存,不入库)。
//
// MCP 工具是远端服务的派生视图(一次 ListTools 即可重建),按"派生数据不入库"原则
// 存进程内存,随同步(启动/新增/手动)整目录重建:
//   - 远端加/删工具 → 下次同步自动收敛,无脏状态、无孤儿行清理;
//   - 描述向量带内容指纹去重缓存,重同步不重嵌入;重启后首轮全量嵌入;
//   - 生效工具 = 连接器开启 ∩ 目录现实 ∩ 用户作用域(共享 + 本人,私有遮蔽共享)。
//
// DB 的 mcp_servers 只存连接器配置与归属;工具无独立启停(跟随连接器开关)。

const (
	// mcpMatchTopK 每轮注入工具数上限。
	mcpMatchTopK = 5
	// mcpMatchThreshold 语义命中阈值(工具匹配要比会话召回更严格,宁缺勿滥)。
	mcpMatchThreshold = 0.4
	// mcpSearchMaxAttempts 每回合 mcp_search 重试上限(运行时强制,非 prompt 约束)。
	mcpSearchMaxAttempts = 3
)

// MCPToolInfo 目录条目:一个 MCP 工具的完整描述。
type MCPToolInfo struct {
	ServerID       int64
	ServerName     string
	OwnerUserID    int64 // 0=共享
	Name           string
	Description    string
	ParamsSchema   string // InputSchema 原始 JSON
	Embedding      []float32
	EmbeddingModel string
}

// mcpEmbedEntry 描述向量缓存(指纹 → 向量),重同步免重嵌入。
type mcpEmbedEntry struct {
	fingerprint string
	vec         []float32
	model       string
}

// MCPToolCatalog 连接器工具目录(进程内)。
type MCPToolCatalog struct {
	mu        sync.RWMutex
	byServer  map[int64][]*MCPToolInfo
	embedCash map[int64]map[string]mcpEmbedEntry // serverID → toolName → entry
}

func newMCPToolCatalog() *MCPToolCatalog {
	return &MCPToolCatalog{
		byServer:  map[int64][]*MCPToolInfo{},
		embedCash: map[int64]map[string]mcpEmbedEntry{},
	}
}

// replace 整目录替换一个 server 的工具(同步原子性:全量重建)。
func (c *MCPToolCatalog) replace(serverID int64, tools []*MCPToolInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byServer[serverID] = tools
}

func (c *MCPToolCatalog) remove(serverID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byServer, serverID)
	delete(c.embedCash, serverID)
}

// snapshot 该用户可见(共享+本人)的全部工具。
func (c *MCPToolCatalog) snapshot(userID int64) []*MCPToolInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []*MCPToolInfo
	for _, tools := range c.byServer {
		for _, t := range tools {
			if t.OwnerUserID == 0 || t.OwnerUserID == userID {
				out = append(out, t)
			}
		}
	}
	return out
}

// MCPService 上的目录装配与检索。

// replaceServerCatalog 同步后整目录重建:ListTools 结果 → 向量化 → 替换。返回工具数。
func (s *MCPService) replaceServerCatalog(serverRow *mcpdomain.MCPServer, tools []mcp.Tool) int {
	provider := s.providerFor(ptrDeref(serverRow.UserID))
	infos := make([]*MCPToolInfo, 0, len(tools))
	s.catalog.mu.Lock()
	cache := s.catalog.embedCash[serverRow.ID]
	if cache == nil {
		cache = map[string]mcpEmbedEntry{}
	}
	for _, tool := range tools {
		info := &MCPToolInfo{
			ServerID:     serverRow.ID,
			ServerName:   serverRow.Name,
			OwnerUserID:  ptrDeref(serverRow.UserID),
			Name:         tool.Name,
			Description:  tool.Description,
			ParamsSchema: marshalSchema(tool.InputSchema),
		}
		// 描述向量化(指纹去重:重同步未变不重嵌;provider 变更后指纹判等失效自动重嵌)
		if provider != nil && strings.TrimSpace(tool.Description) != "" {
			sum := sha1.Sum([]byte(tool.Name + "|" + tool.Description))
			fp := hex.EncodeToString(sum[:])
			if hit, ok := cache[tool.Name]; ok && hit.fingerprint == fp && hit.model == provider.Name() {
				info.Embedding, info.EmbeddingModel = hit.vec, hit.model
			} else if vecs, err := provider.Embed(context.Background(), []string{tool.Name + "\n" + tool.Description}); err == nil && len(vecs) == 1 {
				info.Embedding, info.EmbeddingModel = vecs[0], provider.Name()
				cache[tool.Name] = mcpEmbedEntry{fingerprint: fp, vec: vecs[0], model: provider.Name()}
			} else {
				fmt.Printf("[skill] mcp tool %q (server %s) 向量化失败,不参与语义匹配: %v\n", tool.Name, serverRow.Name, err)
			}
		}
		infos = append(infos, info)
	}
	s.catalog.embedCash[serverRow.ID] = cache
	s.catalog.mu.Unlock()
	s.catalog.replace(serverRow.ID, infos)
	return len(infos)
}

func ptrDeref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func marshalSchema(schema mcp.ToolInputSchema) string {
	b, err := json.Marshal(schema)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// matchTools 语义匹配用户可见且连接器开启的工具,余弦降序取 topK(只比同模型向量)。
func (s *MCPService) matchTools(ctx context.Context, userID int64, query string, topK int) ([]*MCPToolInfo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	provider := s.providerFor(userID)
	if provider == nil {
		return nil, nil
	}
	vecs, err := provider.Embed(ctx, []string{query})
	if err != nil || len(vecs) != 1 {
		return nil, fmt.Errorf("匹配向量化失败: %w", err)
	}
	currentModel := provider.Name()

	candidates := s.catalog.snapshot(userID)
	// 连接器总开关过滤:按 server 归属与开启状态
	enabledServer := map[int64]bool{}
	for _, row := range s.visibleServerRows(userID) {
		if row.Enabled {
			enabledServer[row.ID] = true
		}
	}
	type hit struct {
		info  *MCPToolInfo
		score float64
	}
	var hits []hit
	for _, info := range candidates {
		if !enabledServer[info.ServerID] || info.EmbeddingModel != currentModel || len(info.Embedding) == 0 {
			continue
		}
		if score := memoryservice.CosineSimilarity(vecs[0], info.Embedding); score >= mcpMatchThreshold {
			hits = append(hits, hit{info: info, score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	out := make([]*MCPToolInfo, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.info)
	}
	return out, nil
}

// resolveUserMCPTool mcp_call 的工具解析:用户作用域内按原始工具名查找(私有遮蔽共享)。
func (s *MCPService) resolveUserMCPTool(userID int64, toolName string) (*MCPToolInfo, *mcpdomain.MCPServer, error) {
	candidates := s.catalog.snapshot(userID)
	var found *MCPToolInfo
	for _, info := range candidates {
		if info.Name != toolName {
			continue
		}
		if found == nil || (found.OwnerUserID == 0 && info.OwnerUserID == userID) {
			found = info // 私有优先于共享;同类按先到
		}
	}
	if found == nil {
		return nil, nil, fmt.Errorf("当前没有可用的工具 %q;可用 mcp_search 查看现有能力,或如实告知用户", toolName)
	}
	serverRow, err := s.serverRepo.GetByID(found.ServerID)
	if err != nil || serverRow == nil || !serverRow.Enabled {
		return nil, nil, fmt.Errorf("工具 %q 所属连接器不可用", toolName)
	}
	if serverRow.UserID != nil && *serverRow.UserID != userID {
		return nil, nil, fmt.Errorf("工具 %q 不可用", toolName) // 私有 server 不外泄存在性
	}
	return found, serverRow, nil
}

// visibleServerRows 该用户可见的 server 行(共享 + 本人)。
func (s *MCPService) visibleServerRows(userID int64) []*mcpdomain.MCPServer {
	rows, err := s.serverRepo.List()
	if err != nil {
		return nil
	}
	out := make([]*mcpdomain.MCPServer, 0, len(rows))
	for _, r := range rows {
		if r.UserID == nil || *r.UserID == userID {
			out = append(out, r)
		}
	}
	return out
}

// BuildMCPContextBlock 生成晚置注入的「MCP 连接器可用工具」system 消息文本。无命中返回空。
func (s *MCPService) BuildMCPContextBlock(ctx context.Context, userID int64, query string) (string, error) {
	rows, err := s.matchTools(ctx, userID, query, mcpMatchTopK)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	var sb strings.Builder
	sb.WriteString("以下是与当前问题语义匹配的 MCP 连接器可用工具(用 mcp_call 调用;没有需要的能力可用 mcp_search 更换关键词搜索):\n\n")
	for _, r := range rows {
		fmt.Fprintf(&sb, "【%s】%s\n参数说明: %s\n\n", r.Name, r.Description, compactSchema(r.ParamsSchema))
	}
	return sb.String(), nil
}

// compactSchema 压缩渲染参数 schema(注入 token 预算友好:只留 properties/required)。
func compactSchema(schemaJSON string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(schemaJSON), &m); err != nil {
		return schemaJSON
	}
	out := map[string]interface{}{}
	if p, ok := m["properties"]; ok {
		out["properties"] = p
	}
	if r, ok := m["required"]; ok {
		out["required"] = r
	}
	b, err := json.Marshal(out)
	if err != nil {
		return schemaJSON
	}
	return string(b)
}

// specForServer 连接器行 → 客户端连接配置(解密 key;调用时现场握手用)。
func (s *MCPService) specForServer(row *mcpdomain.MCPServer) (*MCPServerSpec, error) {
	spec := &MCPServerSpec{Name: row.Name, BaseURL: row.BaseURL, Enabled: row.Enabled, AuthType: row.AuthType, Transport: row.Transport}
	switch row.AuthType {
	case mcpdomain.AuthTypeOAuth:
		return nil, fmt.Errorf("OAuth 连接器的工具调用走授权客户端路径,暂不支持 mcp_call 直调")
	default: // bearer / none / query
		apiKey, err := decryptSecret(row.APIKey)
		if err != nil {
			return nil, fmt.Errorf("密钥解密失败: %w", err)
		}
		spec.APIKey = apiKey
	}
	return spec, nil
}

// mcpContentText 抽取 CallToolResult 文本内容。
func mcpContentText(result *mcp.CallToolResult) string {
	if result == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// CreateMCPCallTool 构造 mcp_call 元工具(tools 参数恒定项,缓存稳定)。
func (s *MCPService) CreateMCPCallTool() agentpkg.Tool {
	return agentpkg.Tool{
		Name:         "mcp_call",
		DisplayLabel: "调用外部工具",
		Description:  mcpCallDescription,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tool": map[string]interface{}{
					"type":        "string",
					"description": "工具卡片中的工具名称(如 maps_weather)",
				},
				"arguments": map[string]interface{}{
					"type":        "object",
					"description": "按该工具参数说明构造的 JSON 对象",
				},
			},
			"required": []string{"tool"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			userID := agentpkg.GetUserIDFromContext(ctx)
			toolName, _ := args["tool"].(string)
			if strings.TrimSpace(toolName) == "" {
				return "", fmt.Errorf("tool 不能为空")
			}
			callArgs, _ := args["arguments"].(map[string]interface{})
			info, serverRow, err := s.resolveUserMCPTool(userID, toolName)
			if err != nil {
				return "", err
			}
			return s.invokeMCPTool(ctx, serverRow, info.Name, callArgs)
		},
	}
}

// mcpCallDescription mcp_call 描述(告知匹配机制与失败纪律)。
const mcpCallDescription = `调用 MCP 连接器提供的具体工具。可用工具来自两处:
①上下文注入的「MCP 连接器可用工具」块(按当前问题语义匹配所得,含名称/描述/参数说明);
②用 mcp_search 搜索到的工具卡片。
调用时 tool 填工具卡片中的名称,arguments 按该工具的参数说明构造 JSON(必填参数不可缺)。
若调用报参数错误,按报错修正后可重试;若工具不存在或与需求不符,如实告知用户,不要编造工具或参数。`

// mcpSearchDescription mcp_search 描述(重试纪律:换词重试,上限 3 次,到顶必须停止)。
const mcpSearchDescription = `按语义搜索当前可用的 MCP 连接器工具(远程扩展能力,如地图/天气/导航等)。
使用时机:上下文注入的「MCP 连接器可用工具」块中没有你需要的能力时,更换同义关键词搜索(如"查上海天气"→"天气查询")。
返回工具卡片(名称/描述/参数说明),确认后用 mcp_call 调用。
纪律:每回合最多搜索 3 次;达到上限后必须停止重试,如实告知用户当前无法完成该操作。`

// invokeMCPTool 现场握手建连调用(无常驻连接:重启/断线不漂移)。
func (s *MCPService) invokeMCPTool(ctx context.Context, serverRow *mcpdomain.MCPServer, toolName string, callArgs map[string]interface{}) (string, error) {
	spec, err := s.specForServer(serverRow)
	if err != nil {
		return "", fmt.Errorf("连接器配置错误: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, MCPToolTimeout)
	defer cancel()

	mcpClient, ferr := s.mcpFactory(*spec)
	if ferr != nil {
		return "", fmt.Errorf("连接器连接失败: %v", ferr)
	}
	if err := mcpClient.Start(callCtx); err != nil {
		return "", fmt.Errorf("连接器连接失败: %v", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "omnibot", Version: "1.0"}
	if _, err := mcpClient.Initialize(callCtx, initReq); err != nil {
		return "", fmt.Errorf("连接器初始化失败: %v", err)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	if callArgs != nil {
		req.Params.Arguments = callArgs
	}
	result, err := mcpClient.CallTool(callCtx, req)
	if err != nil {
		return "", fmt.Errorf("工具调用失败(%s): %v", toolName, err)
	}
	if result.IsError {
		return "", fmt.Errorf("工具执行报错(%s): %s", toolName, mcpContentText(result))
	}
	return mcpContentText(result), nil
}

// CreateMCPSearchTool 构造 mcp_search 元工具(每回合 3 次上限由 runtime 计数器强制)。
func (s *MCPService) CreateMCPSearchTool() agentpkg.Tool {
	return agentpkg.Tool{
		Name:         "mcp_search",
		DisplayLabel: "搜索外部工具",
		Description:  mcpSearchDescription,
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "语义搜索词,描述你想完成的事(如\"查询城市天气\"\"规划驾车路线\")",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			if counter := agentpkg.MCPSearchCounter(ctx); counter != nil {
				if n := atomic.AddInt32(counter, 1); n > mcpSearchMaxAttempts {
					return "", fmt.Errorf("本回合 MCP 搜索已达上限(%d 次)。请停止重试,如实告知用户当前无法完成该操作", mcpSearchMaxAttempts)
				}
			}
			userID := agentpkg.GetUserIDFromContext(ctx)
			query, _ := args["query"].(string)
			rows, err := s.matchTools(ctx, userID, query, mcpMatchTopK)
			if err != nil {
				return "", fmt.Errorf("搜索失败: %w", err)
			}
			if len(rows) == 0 {
				return "没有找到语义相关的工具。可更换更通用的关键词再试(注意每回合上限);若确定无此能力,请如实告知用户。", nil
			}
			var sb strings.Builder
			sb.WriteString("找到以下相关工具(用 mcp_call 调用):\n\n")
			for _, r := range rows {
				fmt.Fprintf(&sb, "【%s】%s\n参数说明: %s\n\n", r.Name, r.Description, compactSchema(r.ParamsSchema))
			}
			return sb.String(), nil
		},
	}
}
