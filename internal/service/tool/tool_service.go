package tool

import (
	"fmt"
	"sort"
	"sync"

	tooldomain "omnibot/internal/domain/tool"
	"omnibot/internal/pkg/toolcore"
)

// ToolBuilder 工具执行体的构造器(builtin):返回带 Execute 闭包的工具。
// 现有 agent.CreateXXXTool 工厂即 builder——定义与执行体同源,避免漂移(13-技术方案 §5.1)。
type ToolBuilder func() toolcore.Tool

// ToolView 面向 API 的工具视图。
type ToolView struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	// Available 执行体可用(builtin:builder 已注册)。
	// false 时工具隐藏于运行时 registry,界面上展示"不可用"。
	Available bool `json:"available"`
}

// ToolRepository 工具持久化窄接口(service 层声明,repository 层实现)。
type ToolRepository interface {
	UpsertBuiltin(def tooldomain.ToolDef) error
	List() ([]*tooldomain.Tool, error)
	GetByName(name string) (*tooldomain.Tool, error)
	SetEnabled(name string, enabled bool) error
}

// ToolService 工具调度中枢:定义落库(可清单/启停),运行时 registry 由它构建。
// 框架工具(request_input/delegate/mcp_call 等)不归它管,装配点另行注册。
// MCP 连接器管理见 service/mcp(MCPService);Skill 概念留白给未来能力包。
type ToolService struct {
	repo        ToolRepository
	mu          sync.RWMutex
	builders    map[string]ToolBuilder
	mainVisible map[string]bool
	main        *toolcore.ToolRegistry
	global      *toolcore.ToolRegistry
}

func NewToolService(repo ToolRepository) *ToolService {
	return &ToolService{
		repo:        repo,
		builders:    make(map[string]ToolBuilder),
		mainVisible: make(map[string]bool),
	}
}

// RegisterBuiltin 注册内置工具 builder(装配期调用;重名 panic——装配期错误显性)。
// 默认主 Agent 可见。
func (s *ToolService) RegisterBuiltin(builder ToolBuilder) {
	s.registerBuiltin(builder, true)
}

// RegisterBuiltinSubOnly 注册子 Agent 专属工具 builder(主 Agent 不可见)。
// 如抓取类 rss/web_read——方向 B:主 Agent 是管家,联网抓取必须 delegate 派活。
func (s *ToolService) RegisterBuiltinSubOnly(builder ToolBuilder) {
	s.registerBuiltin(builder, false)
}

func (s *ToolService) registerBuiltin(builder ToolBuilder, mainVisible bool) {
	name := builder().Name
	if _, exists := s.builders[name]; exists {
		panic(fmt.Sprintf("tool: builtin builder %q already registered", name))
	}
	s.builders[name] = builder
	s.mainVisible[name] = mainVisible
}

// BindRegistries 绑定运行时的两个工具池(主 Agent 池 + 子 Agent 全局池)。
// 绑定后 SetEnabled 的启停立即应用到这两个池(停用即时生效)。
func (s *ToolService) BindRegistries(main, global *toolcore.ToolRegistry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.main, s.global = main, global
	return nil
}

// SeedBuiltins 把已注册的内置工具定义 upsert 进 tools 表。
// 发版重复调用安全:仅更新定义字段,不碰用户启停状态。
func (s *ToolService) SeedBuiltins() error {
	names := make([]string, 0, len(s.builders))
	for name := range s.builders {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		tool := s.builders[name]()
		if err := s.repo.UpsertBuiltin(tooldomain.ToolDef{
			Name:         tool.Name,
			DisplayName:  tool.DisplayLabel,
			Description:  tool.Description,
			Capabilities: tool.Capabilities,
			Parameters:   tool.Parameters,
			MainVisible:  s.mainVisible[name],
		}); err != nil {
			return fmt.Errorf("tool: seed builtin %q: %w", name, err)
		}
	}
	return nil
}

// List 工具清单(按名称排序,保证确定性)。
func (s *ToolService) List() ([]ToolView, error) {
	rows, err := s.repo.List()
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	views := make([]ToolView, 0, len(rows))
	for _, row := range rows {
		_, hasBuilder := s.builders[row.Name]
		views = append(views, ToolView{
			Name:        row.Name,
			DisplayName: row.DisplayName,
			Description: row.Description,
			Enabled:     row.Enabled,
			Available:   hasBuilder,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views, nil
}

// SetEnabled 启停工具:落库后立即应用到已绑定的运行时 registry(停用即时生效)。
func (s *ToolService) SetEnabled(name string, enabled bool) error {
	if err := s.repo.SetEnabled(name, enabled); err != nil {
		return fmt.Errorf("tool: set enabled %q: %w", name, err)
	}
	s.mu.Lock()
	main, global := s.main, s.global
	s.mu.Unlock()
	if main == nil || global == nil {
		return nil // 尚未绑定(如纯 API 场景),无运行时池可应用
	}
	return s.ApplyTo(main, global)
}

// ApplyTo 幂等重建:对工具名集合——先从两池移除,再把 enabled∧执行体可用的加回。
// 不碰注册在池里的框架工具(名字不属于工具集,天然不受影响)。
func (s *ToolService) ApplyTo(main, global *toolcore.ToolRegistry) error {
	rows, err := s.repo.List()
	if err != nil {
		return fmt.Errorf("tool: list for apply: %w", err)
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
			return fmt.Errorf("tool: apply %q to global: %w", row.Name, err)
		}
		if row.MainVisible {
			if err := main.Register(tool); err != nil {
				return fmt.Errorf("tool: apply %q to main: %w", row.Name, err)
			}
		}
	}
	return nil
}

// buildTool 由 tool 行构造运行时 Tool(builtin:定义以代码 builder 为准)。
func (s *ToolService) buildTool(row *tooldomain.Tool) (toolcore.Tool, bool) {
	s.mu.RLock()
	builder, ok := s.builders[row.Name]
	s.mu.RUnlock()
	if ok {
		return builder(), true
	}
	return toolcore.Tool{}, false
}
