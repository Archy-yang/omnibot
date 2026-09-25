package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	memoryservice "omnibot/internal/service/memory"
)

// MCPService MCP 连接器调度中枢:server 配置管理(增删改查/同步/OAuth 授权)
// + 工具目录(内存缓存,不入库)+ mcp_call/mcp_search 元工具供给。
// 工具定义落库管理见 service/tool(ToolService);两者按概念分家(13-技术方案)。
type MCPService struct {
	serverRepo MCPServerRepository
	mcpFactory MCPClientFactory
	mu         sync.RWMutex

	// OAuth 运行态(M4)
	oauthRedirectBase string // 回调基址(装配点注入 app.external_url)
	pendingMu         sync.RWMutex
	pendingOAuth      map[string]*pendingOAuth // state → 进行中的授权流程

	// B2 语义匹配:embedding provider(系统默认 + 用户级解析,与沉淀/召回同模式)
	embedding         memoryservice.EmbeddingProvider
	embeddingResolver func(userID int64) memoryservice.EmbeddingProvider
	// MCP 工具目录(内存缓存,不入库;见 mcp_catalog.go)
	catalog *MCPToolCatalog
}

// NewMCPService 创建 MCP 连接器服务。
func NewMCPService() *MCPService {
	return &MCPService{
		pendingOAuth: make(map[string]*pendingOAuth),
		catalog:      newMCPToolCatalog(),
	}
}

// SetMCPClientFactory 注入客户端工厂(装配/测试用)。
func (s *MCPService) SetMCPClientFactory(f MCPClientFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpFactory = f
}

// SetEmbeddingProvider 注入系统默认向量 provider(可空)。
func (s *MCPService) SetEmbeddingProvider(p memoryservice.EmbeddingProvider) { s.embedding = p }

// SetEmbeddingResolver 注入用户级向量解析(非 nil 且返回非 nil 时优先)。
func (s *MCPService) SetEmbeddingResolver(r func(userID int64) memoryservice.EmbeddingProvider) {
	s.embeddingResolver = r
}

// providerFor 取生效 provider(匹配同模型向量)。
func (s *MCPService) providerFor(userID int64) memoryservice.EmbeddingProvider {
	if s.embeddingResolver != nil {
		if p := s.embeddingResolver(userID); p != nil {
			return p
		}
	}
	return s.embedding
}

// embedToolDesc 描述向量化;失败返回空(不参与匹配,重同步重试)。
func (s *MCPService) embedToolDesc(provider memoryservice.EmbeddingProvider, toolName, desc, serverName string) ([]float32, string) {
	if provider == nil || strings.TrimSpace(desc) == "" {
		return nil, ""
	}
	vecs, err := provider.Embed(context.Background(), []string{toolName + "\n" + desc})
	if err != nil || len(vecs) != 1 {
		fmt.Printf("[mcp] tool %q (server %s) 向量化失败,不参与语义匹配: %v\n", toolName, serverName, err)
		return nil, ""
	}
	return vecs[0], provider.Name()
}
