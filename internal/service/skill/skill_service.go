package skill

import (
	"fmt"
	"sort"
	"sync"

	skilldomain "omnibot/internal/domain/skill"
	agentpkg "omnibot/internal/service/agent"
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
	// UpsertMCPTool upsert MCP 发现的远端工具:插入默认停用,更新定义字段不碰 Enabled。
	UpsertMCPTool(def skilldomain.MCPToolDef) error
	// DeleteMCPSkillsNotIn 清理不在配置内的 MCP server 的技能行(配置移除后)。
	DeleteMCPSkillsNotIn(serverNames []string) (int64, error)
	// DeleteMCPSkillsByServer 删除指定 server 的全部技能行(server 删除级联/重新同步)。
	DeleteMCPSkillsByServer(serverName string) (int64, error)
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
	mcpExecutors map[string]mcpExecutor
	main         *agentpkg.ToolRegistry
	global       *agentpkg.ToolRegistry
}

func NewSkillService(repo SkillRepository) *SkillService {
	return &SkillService{
		repo:         repo,
		builders:     make(map[string]ToolBuilder),
		mainVisible:  make(map[string]bool),
		mcpExecutors: make(map[string]mcpExecutor),
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

// buildTool 由 skill 行构造运行时 Tool。
// builtin:定义以代码 builder 为准(与执行体同源,发版即更新);
// mcp:定义以库内行为准(同步自 ListTools),执行体来自 CallTool 闭包;
// 执行体缺失 → false,技能隐藏(13-技术方案 §3 原则 3)。
func (s *SkillService) buildTool(row *skilldomain.Skill) (agentpkg.Tool, bool) {
	s.mu.RLock()
	builder, ok := s.builders[row.Name]
	s.mu.RUnlock()
	if ok {
		return builder(), true
	}
	return s.buildMCPTool(row)
}
