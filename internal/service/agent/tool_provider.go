package agent

import (
	"fmt"
	"sort"
)

// DefaultSubAgentCapabilities 默认子 Agent 能力白名单。
// 复刻原 researcher 卡工具集(research 类)+ request_input(interactive 基线):rss_reader/web_read/search_memories/search_history。
// 装配点(cfg.Agent.SubAgent.AllowedCapabilities)为空时回落此默认。
var DefaultSubAgentCapabilities = []string{CapResearch, CapInteractive}

// ToolProviderResult 子 Agent 工具可见性解析结果(仿 DSH ToolProviderResult:knownNames vs schemas)。
//
// KnownNames = 子 Agent 工具可及的全集(全局工具池全部工具名),供配置校验区分"拼错"与"故意隐藏";
// Visible    = 本次实际对子 Agent 可见的工具名(能力 ∩ 白名单 + 强制基线)。
type ToolProviderResult struct {
	KnownNames []string
	Visible    []string
}

// alwaysBaselineTool 任何 scope 下都强制可见的工具名常量(capability 白名单无法屏蔽)。
// 等价 DSH 全局 tool provider 恒贡献:request_input 是子 Agent 与用户/主 Agent 交互的生存依赖(#19),已被批复用于任务挂起/补料。
const alwaysBaselineTool = "request_input"

// frameworkCapabilities 框架能力词汇表(取值域),capability 白名单的拼写校验据此判定。
// 注意:它不是"当前注册工具所带能力"的并集。默认白名单会引用一个当前可能无工具承载的能力(request_input 的
// interactive)——即便某进程未注册 request_input,config 写 "interactive" 也是合法的,不应做配错报错。
// (仿 DSH:knownNames 是已知工具名全集,而非"当前可见"子集;拼错的是它不在全集里,而非"此处没加载")
var frameworkCapabilities = []string{CapBasic, CapMemory, CapResearch, CapWeb, CapIngest, CapInteractive}

// ResolveSubAgentTools 由全局工具池 + 能力白名单解析子 Agent 可见工具集。
//
// 规则(仿 DSH knownNames/schemas 语义):
//   - known   = 全局池全部工具名
//   - visible = 能力 ∩ allowed 命中者 ∪ alwaysBaselineTool(常驻)
//   - allowed 含未知 capability → 返回 error(配置拼错显性),故 produced Visible/KnownNames 在错误时不可用
//
// 返回的两组名均按字典序排序,保证确定性、便于断言。
func ResolveSubAgentTools(global *ToolRegistry, allowed []string) (ToolProviderResult, error) {
	all := global.ListAll()

	// 用框架能力词汇表(而非当前注册工具所带能力)校验白名单拼写:
	// 默认白名单引用的 interactive(request_input 基线)可能当前无工具承载,但配置合法,不报错。
	knownCaps := map[string]struct{}{}
	for _, c := range frameworkCapabilities {
		knownCaps[c] = struct{}{}
	}
	// 白名单里出现未知能力 → 配错(仿 DSH:knownNames 里没有的名字是拼错,报错),而非静默
	allowedSet := map[string]struct{}{}
	for _, c := range allowed {
		if _, ok := knownCaps[c]; !ok {
			return ToolProviderResult{}, fmt.Errorf("sub agent tool provider: unknown capability %q", c)
		}
		allowedSet[c] = struct{}{}
	}

	known := make([]string, 0, len(all))
	visible := make([]string, 0, len(all))
	seen := map[string]struct{}{}
	for _, t := range all {
		known = append(known, t.Name)
		matches := false
		if len(t.Capabilities) > 0 {
			for _, c := range t.Capabilities {
				if _, ok := allowedSet[c]; ok {
					matches = true
					break
				}
			}
		}
		if t.Name == alwaysBaselineTool {
			matches = true // 强制基线,任何白名单都可见(修复 #19 request_input 未注入子 Agent 的间隙)
		}
		if matches {
			visible = append(visible, t.Name)
			seen[t.Name] = struct{}{}
		}
	}

	sort.Strings(known)
	sort.Strings(visible)
	return ToolProviderResult{KnownNames: known, Visible: visible}, nil
}

// BuildSubAgentToolRegistry 按可见集从全局池构建子 Agent 独立 ToolRegistry(供 SubAgentRunner.Run 用)。
// 任一可见工具在全局池缺失是内部不一致 bug,返回 error 而非静默。
func BuildSubAgentToolRegistry(global *ToolRegistry, allowed []string) (*ToolRegistry, ToolProviderResult, error) {
	res, err := ResolveSubAgentTools(global, allowed)
	if err != nil {
		return nil, ToolProviderResult{}, err
	}
	sub := NewToolRegistry()
	for _, name := range res.Visible {
		tool, ok := global.Get(name)
		if !ok {
			return nil, ToolProviderResult{}, fmt.Errorf("sub agent tool provider: visible tool %q not in global pool", name)
		}
		if err := sub.Register(tool); err != nil {
			return nil, ToolProviderResult{}, fmt.Errorf("sub agent tool provider: register %q: %w", name, err)
		}
	}
	return sub, res, nil
}
