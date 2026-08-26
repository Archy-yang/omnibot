package agentprompt

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ScopeKey 作用域键:决定哪些 section 参与某次组装(11-Prompt管理技术方案 §3.1)。
// ""(空串)=全局,所有组装都包含。
type ScopeKey string

// 主 Agent 作用域。子 Agent 用 SubScope(type) 构造。
const ScopeMain ScopeKey = "main"

// SubScope 构造子 Agent 作用域键(如 "sub:researcher")。
func SubScope(agentType string) ScopeKey { return ScopeKey("sub:" + agentType) }

// PromptCtx 一次 Assemble 的请求:要谁(命中的作用域) + 本次运行的动态数据(§3.2)。
type PromptCtx struct {
	Scopes  []ScopeKey        // 命中的作用域
	Request map[string]string // 本次运行动态数据(goal/deliverables/用户配置等,已格式化文本)
}

// Provider 每次组装按请求上下文求值该 section 的动态文本(§3.3)。
type Provider func(c PromptCtx) string

// PromptSection 一条可注册的提示词片段(§3.3)。
type PromptSection struct {
	Name     string    // (Scope, Name) 唯一;同作用域内重复注册抛错
	Scope    ScopeKey  // "" = 全局,所有组装参与
	Order    int       // 排序,升序拼接
	Text     string    // 静态文本;Text 与 Provider 二选一
	Provider Provider  // 动态求值;两者都空则该 section 不产文本
	Complete bool      // true = 该 section 独占整个 system prompt(逃逸口,§6)
}

// StaticSection 构造一个静态 PromptSection。
func StaticSection(name string, scope ScopeKey, order int, text string) PromptSection {
	return PromptSection{Name: name, Scope: scope, Order: order, Text: text}
}

// DynamicSection 构造一个按 Provider 动态求值的 PromptSection。
func DynamicSection(name string, scope ScopeKey, order int, provider Provider) PromptSection {
	return PromptSection{Name: name, Scope: scope, Order: order, Provider: provider}
}

// Variable 对 {{name}} 占位的插值 provider,按请求上下文求值(§3.4)。
type Variable func(c PromptCtx) (string, error)

// PromptRegistry 注册中心 + 组装入口(§3.4)。
type PromptRegistry struct {
	sections  []PromptSection
	variables map[string]Variable
}

// NewPromptRegistry 创建注册中心。
func NewPromptRegistry() *PromptRegistry {
	return &PromptRegistry{variables: make(map[string]Variable)}
}

// Register 注册 section。(Scope, Name) 重复抛错(§3.4)。
func (r *PromptRegistry) Register(s PromptSection) error {
	for _, other := range r.sections {
		if other.Scope == s.Scope && other.Name == s.Name {
			return fmt.Errorf("prompt: section %q(scope=%q) 重复注册", s.Name, s.Scope)
		}
	}
	r.sections = append(r.sections, s)
	return nil
}

// 变量名约束:小写字母开头,后续小写字母/数字/下划线(与 DSH 一致)。
var variableNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// RegisterVariable 注册 {{name}} 插值 provider。name 须匹配 [a-z][a-z0-9_]*。
func (r *PromptRegistry) RegisterVariable(name string, fn Variable) error {
	if !variableNameRe.MatchString(name) {
		return fmt.Errorf("prompt: 非法变量名 %q(须匹配 [a-z][a-z0-9_]*)", name)
	}
	if _, exists := r.variables[name]; exists {
		return fmt.Errorf("prompt: 变量 %q 重复注册", name)
	}
	r.variables[name] = fn
	return nil
}

// Has 查询某 section 是否已注册(替换 hasSubAgents 这类布尔判断,§3.4)。
// 只按 (scope, name) 精确匹配,不做作用域包含推断。
func (r *PromptRegistry) Has(scope ScopeKey, name string) bool {
	for _, s := range r.sections {
		if s.Scope == scope && s.Name == name {
			return true
		}
	}
	return false
}

// Assemble 组装最终 system prompt(§4):
//  1. 收集参与 section(全局 + 命中 c.Scopes)
//  2. Complete 逃逸口:>1 条报错;恰好 1 条则只渲染它
//  3. 按 Order 升序稳定排序
//  4. 逐条取文本(Provider 优先),空文本跳过,拼接
//  5. 插值 {{var}}(缺失抛错)
func (r *PromptRegistry) Assemble(c PromptCtx) (string, error) {
	var participating []PromptSection
	completeCount := 0
	for _, s := range r.sections {
		if s.Scope != "" && !containsScope(c.Scopes, s.Scope) {
			continue
		}
		participating = append(participating, s)
		if s.Complete {
			completeCount++
		}
	}

	if completeCount > 1 {
		return "", fmt.Errorf("prompt: %d 条 complete section,最多允许 1 条", completeCount)
	}

	if completeCount == 1 {
		for _, s := range participating {
			if !s.Complete {
				continue
			}
			text := renderSection(s, c)
			if text == "" {
				continue
			}
			return r.interpolate(c, text)
		}
	}

	sort.SliceStable(participating, func(i, j int) bool {
		return participating[i].Order < participating[j].Order
	})
	var b strings.Builder
	for _, s := range participating {
		if text := renderSection(s, c); text != "" {
			b.WriteString(text)
		}
	}
	return r.interpolate(c, b.String())
}

// renderSection 取一条 section 的文本:Provider 优先于 Text。
func renderSection(s PromptSection, c PromptCtx) string {
	if s.Provider != nil {
		return s.Provider(c)
	}
	return s.Text
}

func containsScope(scopes []ScopeKey, target ScopeKey) bool {
	for _, s := range scopes {
		if s == target {
			return true
		}
	}
	return false
}

// 插值占位模式:{{name}}。
var interpRe = regexp.MustCompile(`\{\{([a-z][a-z0-9_]*)\}\}`)

// interpolate 把整段文本里的 {{name}} 替换为注册变量的求值结果。缺失变量抛错。
func (r *PromptRegistry) interpolate(c PromptCtx, text string) (string, error) {
	idxs := interpRe.FindAllStringSubmatchIndex(text, -1)
	if len(idxs) == 0 {
		return text, nil
	}
	var b strings.Builder
	last := 0
	for _, idx := range idxs {
		b.WriteString(text[last:idx[0]])
		name := text[idx[2]:idx[3]]
		fn, ok := r.variables[name]
		if !ok {
			return "", fmt.Errorf("prompt: 引用未注册变量 {{%s}}", name)
		}
		v, err := fn(c)
		if err != nil {
			return "", fmt.Errorf("prompt: 变量 {{%s}} 求值失败: %w", name, err)
		}
		b.WriteString(v)
		last = idx[1]
	}
	b.WriteString(text[last:])
	return b.String(), nil
}