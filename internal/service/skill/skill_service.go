package skill

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	skilldomain "omnibot/internal/domain/skill"
	agentpkg "omnibot/internal/service/agent"
	memoryservice "omnibot/internal/service/memory"
)

// ToolBuilder 技能执行体的构造器(builtin):返回带 Execute 闭包的工具。
// 现有 agent.CreateXXXTool 工厂即 builder——定义与执行体同源,避免漂移(13-技术方案 §5.1)。
type ToolBuilder func() agentpkg.Tool

// SkillView 面向 API 的技能视图。
type SkillView struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Enabled     bool   `json:"enabled"`
	// Available 执行体可用(builtin:builder 已注册;mcp M2:server 在线)。
	// false 时技能隐藏于运行时 registry,界面上展示"不可用"。
	Available bool `json:"available"`
}

// SkillRepository 技能持久化窄接口(service 层声明,repository 层实现)。
type SkillRepository interface {
	UpsertBuiltin(def skilldomain.BuiltinDef) error
	// DeleteAllMCPSkills 清理全部 source=mcp 技能行(目录化后的一次性清废)。
	DeleteAllMCPSkills() (int64, error)
	List() ([]*skilldomain.Skill, error)
	GetByName(name string) (*skilldomain.Skill, error)
	SetEnabled(name string, enabled bool) error
}

// skillRepo 带锁读取技能仓储。
func (s *SkillService) skillRepo() SkillRepository {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.repo
}

// SkillService 技能调度中枢:定义落库(可清单/启停),运行时 registry 由它构建。
// 框架工具(request_input/delegate 等)不归它管,装配点另行注册。
type SkillService struct {
	repo         SkillRepository
	serverRepo   MCPServerRepository
	mu           sync.RWMutex
	builders     map[string]ToolBuilder
	mainVisible  map[string]bool
	mcpFactory   MCPClientFactory
	main         *agentpkg.ToolRegistry
	global       *agentpkg.ToolRegistry

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

// SetMCPClientFactory 注入客户端工厂(装配/测试用)。
func (s *SkillService) SetMCPClientFactory(f MCPClientFactory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mcpFactory = f
}

// SetEmbeddingProvider 注入系统默认向量 provider(可空)。
func (s *SkillService) SetEmbeddingProvider(p memoryservice.EmbeddingProvider) { s.embedding = p }

// SetEmbeddingResolver 注入用户级向量解析(非 nil 且返回非 nil 时优先)。
func (s *SkillService) SetEmbeddingResolver(r func(userID int64) memoryservice.EmbeddingProvider) {
	s.embeddingResolver = r
}

// providerFor 取生效 provider(匹配同模型向量)。
func (s *SkillService) providerFor(userID int64) memoryservice.EmbeddingProvider {
	if s.embeddingResolver != nil {
		if p := s.embeddingResolver(userID); p != nil {
			return p
		}
	}
	return s.embedding
}

// embedToolDesc 描述向量化;失败返回空(不参与匹配,重同步重试)。
func (s *SkillService) embedToolDesc(provider memoryservice.EmbeddingProvider, toolName, desc, serverName string) ([]float32, string) {
	if provider == nil || strings.TrimSpace(desc) == "" {
		return nil, ""
	}
	vecs, err := provider.Embed(context.Background(), []string{toolName + "\n" + desc})
	if err != nil || len(vecs) != 1 {
		fmt.Printf("[skill] mcp tool %q (server %s) 向量化失败,不参与语义匹配: %v\n", toolName, serverName, err)
		return nil, ""
	}
	return vecs[0], provider.Name()
}

func NewSkillService(repo SkillRepository) *SkillService {
	return &SkillService{
		repo:         repo,
		builders:     make(map[string]ToolBuilder),
		mainVisible:  make(map[string]bool),
		pendingOAuth: make(map[string]*pendingOAuth),
		catalog:      newMCPToolCatalog(),
	}
}

// RegisterBuiltin 注册内置技能 builder(装配期调用;重名 panic——装配期错误显性)。
// 默认主 Agent 可见。
func (s *SkillService) RegisterBuiltin(builder ToolBuilder) {
	s.registerBuiltin(builder, true)
}

// RegisterBuiltinSubOnly 注册子 Agent 专属技能 builder(主 Agent 不可见)。
// 如抓取类 rss/web_read——方向 B:主 Agent 是管家,联网抓取必须 delegate 派活。
func (s *SkillService) RegisterBuiltinSubOnly(builder ToolBuilder) {
	s.registerBuiltin(builder, false)
}

func (s *SkillService) registerBuiltin(builder ToolBuilder, mainVisible bool) {
	name := builder().Name
	if _, exists := s.builders[name]; exists {
		panic(fmt.Sprintf("skill: builtin builder %q already registered", name))
	}
	s.builders[name] = builder
	s.mainVisible[name] = mainVisible
}

// BindRegistries 绑定运行时的两个工具池(主 Agent 池 + 子 Agent 全局池)。
// 绑定后 SetEnabled 的启停立即应用到这两个池(停用即时生效)。
func (s *SkillService) BindRegistries(main, global *agentpkg.ToolRegistry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.main, s.global = main, global
	return nil
}

// SeedBuiltins 把已注册的内置技能定义 upsert 进 skills 表。
// 发版重复调用安全:仅更新定义字段,不碰用户启停状态。
func (s *SkillService) SeedBuiltins() error {
	names := make([]string, 0, len(s.builders))
	for name := range s.builders {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		tool := s.builders[name]()
		if err := s.repo.UpsertBuiltin(skilldomain.BuiltinDef{
			Name:         tool.Name,
			DisplayName:  tool.DisplayLabel,
			Description:  tool.Description,
			Capabilities: tool.Capabilities,
			Parameters:   tool.Parameters,
			MainVisible:  s.mainVisible[name],
		}); err != nil {
			return fmt.Errorf("skill: seed builtin %q: %w", name, err)
		}
	}
	return nil
}

// List 技能清单(按名称排序,保证确定性)。
func (s *SkillService) List() ([]SkillView, error) {
	rows, err := s.repo.List()
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	views := make([]SkillView, 0, len(rows))
	for _, row := range rows {
		_, hasBuilder := s.builders[row.Name]
		views = append(views, SkillView{
			Name:        row.Name,
			DisplayName: row.DisplayName,
			Description: row.Description,
			Source:      row.Source,
			Enabled:     row.Enabled,
			Available:   hasBuilder,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views, nil
}

// SetEnabled 启停技能:落库后立即应用到已绑定的运行时 registry(停用即时生效)。
func (s *SkillService) SetEnabled(name string, enabled bool) error {
	if err := s.repo.SetEnabled(name, enabled); err != nil {
		return fmt.Errorf("skill: set enabled %q: %w", name, err)
	}
	s.mu.Lock()
	main, global := s.main, s.global
	s.mu.Unlock()
	if main == nil || global == nil {
		return nil // 尚未绑定(如纯 API 场景),无运行时池可应用
	}
	return s.ApplyTo(main, global)
}

// ApplyTo 幂等重建:对技能名集合——先从两池移除,再把 enabled∧执行体可用的加回。
// 不碰注册在池里的框架工具(名字不属于技能集,天然不受影响)。
func (s *SkillService) ApplyTo(main, global *agentpkg.ToolRegistry) error {
	rows, err := s.repo.List()
	if err != nil {
		return fmt.Errorf("skill: list for apply: %w", err)
	}

	for _, row := range rows {
		// 无论启用与否,先移除,保证幂等(先开后关/先关后开都收敛)
		main.Remove(row.Name)
		global.Remove(row.Name)

		// B2:MCP 工具不再进 registry——调用统一走 mcp_call 元工具(按用户作用域解析),
		// 目录行仅作为能力清单参与语义匹配。
		if row.Source == skilldomain.SourceMCP {
			continue
		}
		if !row.Enabled {
			continue
		}
		tool, ok := s.buildTool(row)
		if !ok {
			continue // 执行体缺失/schema 非法 → 隐藏(13-技术方案 §3 原则 3)
		}
		if err := global.Register(tool); err != nil {
			return fmt.Errorf("skill: apply %q to global: %w", row.Name, err)
		}
		if row.MainVisible {
			if err := main.Register(tool); err != nil {
				return fmt.Errorf("skill: apply %q to main: %w", row.Name, err)
			}
		}
	}
	return nil
}

// buildTool 由 skill 行构造运行时 Tool(builtin:定义以代码 builder 为准)。
// MCP 行不再走此路径:工具目录在内存,调用统一走 mcp_call(B2)。
func (s *SkillService) buildTool(row *skilldomain.Skill) (agentpkg.Tool, bool) {
	s.mu.RLock()
	builder, ok := s.builders[row.Name]
	s.mu.RUnlock()
	if ok {
		return builder(), true
	}
	return agentpkg.Tool{}, false
}
