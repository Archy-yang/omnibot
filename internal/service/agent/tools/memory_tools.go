package tools

// memory_tools.go — 记忆检索工具(12-记忆系统 §8;自 agent 包迁入)。
// MemoryProvider/MemorySearcher/MatterSearcher/RecentMessageSearcher 接口随迁,
// agent 核心不感知记忆实现。

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/internal/pkg/toolcore"
)

type MemoryProvider interface {
	GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
}

// MemorySearcher 语义记忆检索(可选增强,12-记忆系统技术方案 §8;22号扩展 filter)。
// MemoryProvider 实现可额外实现此接口;search_memories 工具优先走语义路径,否则老子串兜底。
type MemorySearcher interface {
	SearchMemories(ctx context.Context, userID int64, query string, topK int, filter *memorydomain.MemoryFilter) ([]memorydomain.MemoryHit, error)
}

// MatterByTitle 档案标题解析(22号:search_memories 的 matter_title 参数;可选增强)。
type MatterByTitle interface {
	MatterIDByTitle(userID int64, title string) (int64, bool)
}

// CreateGetCurrentTimeTool 获取当前时间工具

func CreateSearchMemoriesTool(memorySvc MemoryProvider) toolcore.Tool {
	return toolcore.Tool{
		Name: "search_memories",
		Description: "搜索用户的记忆，一次返回三段：①进行中的事项（含当前状态与相关记忆全景，" +
			"适合问\"某件事怎么样了/进展如何\"）；②近期对话原文（带发生时间，适合问\"最近/当时聊了什么\"）；" +
			"③与查询相关的长期记忆条目",
		DisplayLabel: "翻了翻记忆",
		Capabilities: []string{toolcore.CapMemory, toolcore.CapResearch},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "搜索关键词或语义描述",
				},
				"from": map[string]interface{}{
					"type":        "string",
					"description": "可选。时间窗下界(RFC3339);传了会按事件时间升序输出时间线。\"上个月\"等相对时间先自行换算",
				},
				"to": map[string]interface{}{
					"type":        "string",
					"description": "可选。时间窗上界(RFC3339)",
				},
				"matter_title": map[string]interface{}{
					"type":        "string",
					"description": "可选。只看某个主题档案(精确标题)",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			query, ok := args["query"].(string)
			if !ok || query == "" {
				return "", fmt.Errorf("query is required")
			}
			userID := toolcore.UserIDFromContext(ctx)
			filter, ferr := parseMemoryFilter(userID, args, memorySvc)
			if ferr != nil {
				return "", ferr
			}
			if searcher, ok := memorySvc.(MemorySearcher); ok {
				recent, _ := memorySvc.(RecentMessageSearcher) // M7:中期区可选,无实现则省略
				// M7 三段式:事项命中优先(返回"这件事"的全景) → 近期对话原文 → 散点记忆
				if matterSearcher, ok := memorySvc.(MatterSearcher); ok {
					return searchMemoriesMatterFirst(ctx, matterSearcher, recent, searcher, userID, query, filter)
				}
				return searchMemoriesSemantic(ctx, recent, searcher, userID, query, filter)
			}
			memories, err := memorySvc.GetRecentForContext(ctx, userID, 50)
			if err != nil {
				return "", fmt.Errorf("查询记忆失败: %w", err)
			}
			return filterMemories(memories, query), nil
		},
	}
}

// MatterSearcher 事项优先检索(可选增强,M6.2):命中事项时返回其当前状态+关联记忆全景。
type MatterSearcher interface {
	SearchMatters(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MatterHit, error)
}

// RecentMessageSearcher 中期记忆检索(可选增强,M7):原文片段直达,零抽象细节。
type RecentMessageSearcher interface {
	SearchRecentMessages(ctx context.Context, userID int64, query string, topK int, filter *memorydomain.MemoryFilter) ([]memorydomain.MessageHit, error)
}

// searchMemoriesSemantic 语义检索路径:近期对话原文(可选)+ 记忆内容 + 来源/时间标注。
func searchMemoriesSemantic(ctx context.Context, recent RecentMessageSearcher, searcher MemorySearcher, userID int64, query string, filter *memorydomain.MemoryFilter) (string, error) {
	hits, err := searcher.SearchMemories(ctx, userID, query, 10, filter)
	if err != nil {
		return "", fmt.Errorf("查询记忆失败: %w", err)
	}
	if filter != nil && (filter.From != nil || filter.To != nil) {
		// 22号:时间线输出——按有效时间升序重排("这段时间做了什么"的阅读顺序)
		sort.Slice(hits, func(i, j int) bool { return effectiveTimeOf(hits[i].Memory).Before(effectiveTimeOf(hits[j].Memory)) })
	}
	var b strings.Builder
	writeRecentSection(ctx, recent, userID, query, filter, &b)
	if len(hits) == 0 {
		if b.Len() == 0 {
			return "未找到相关记忆", nil
		}
		return strings.TrimRight(b.String(), "\n"), nil
	}
	fmt.Fprintf(&b, "找到 %d 条相关记忆:\n", len(hits))
	for i, h := range hits {
		fmt.Fprintf(&b, "%d. %s%s\n", i+1, h.Memory.Content, memoryAnnotation(h.Memory))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// recentSearchTopK 中期区条数(M7 §10.6):话题稀释实验结论——比长期区宽,邻域命中也可用。
const recentSearchTopK = 8

// writeRecentSection 中期区渲染(M7):近期对话原文直达,带发生时间。
// 无实现/无命中时整段省略。
func writeRecentSection(ctx context.Context, recent RecentMessageSearcher, userID int64, query string, filter *memorydomain.MemoryFilter, b *strings.Builder) {
	if recent == nil {
		return
	}
	msgHits, err := recent.SearchRecentMessages(ctx, userID, query, recentSearchTopK, filter)
	if err != nil || len(msgHits) == 0 {
		return // 中期层静默缺失(§10.5 降级)
	}
	fmt.Fprintf(b, "【近期对话】%d 段相关原文:\n", len(msgHits))
	for _, mh := range msgHits {
		content := strings.ReplaceAll(mh.Content, "\n", " ")
		if len(content) > 120 {
			content = content[:120] + "…"
		}
		fmt.Fprintf(b, "- [%s %s] [#%d] %s\n",
			mh.CreatedAt.Format("2006-01-02 15:04"), mh.Role, mh.MessageID, content)
	}
	b.WriteString("\n")
}

// memoryAnnotation 记忆条目的括号标注:来源(自动/手动)+ 事件日期(happened_at 优先,回落记录时间;
// 22号与 Journal 展示语义一致),让 LLM 回答时能带上"什么时候"而不只"是什么"。
func memoryAnnotation(m *memorydomain.Memory) string {
	parts := make([]string, 0, 2)
	if m.Source == memorydomain.MemorySourceAuto {
		parts = append(parts, "自动记忆")
	}
	if !m.CreatedAt.IsZero() {
		parts = append(parts, effectiveTimeOf(m).Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " · ") + ")"
}

// effectiveTimeOf 记忆的有效时间(21号语义:happened_at ?? created_at)。
func effectiveTimeOf(m *memorydomain.Memory) time.Time {
	if m.HappenedAt != nil {
		return *m.HappenedAt
	}
	return m.CreatedAt
}

// parseMemoryFilter 解析 search_memories 的 from/to/matter_title 可选参数(22号 §4)。
// 无任何参数 → nil(现状行为);matter_title 未命中 → 返回提示文本(err==nil 约定:特殊值由
// 调用方直接输出)不便——改为返回 error? 不:未命中档案是"可恢复的查询语义",返回引导文案。
func parseMemoryFilter(userID int64, args map[string]interface{}, svc MemoryProvider) (*memorydomain.MemoryFilter, error) {
	parse := func(key string) (*time.Time, error) {
		raw, ok := args[key].(string)
		if !ok || raw == "" {
			return nil, nil
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("%s 必须是 RFC3339 格式(如 2026-09-01T00:00:00Z),收到 %q;相对时间请先换算", key, raw)
		}
		return &t, nil
	}
	from, err := parse("from")
	if err != nil {
		return nil, err
	}
	to, err := parse("to")
	if err != nil {
		return nil, err
	}
	filter := &memorydomain.MemoryFilter{From: from, To: to}
	if title, ok := args["matter_title"].(string); ok && title != "" {
		resolver, ok := svc.(MatterByTitle)
		if !ok {
			return filter, nil // 实现未提供解析:忽略该参数(降级)
		}
		id, found := resolver.MatterIDByTitle(userID, title)
		if !found {
			return nil, fmt.Errorf("未找到档案「%s」,可先搜索事项确认标题后再试", title)
		}
		filter.MatterID = &id
	}
	if filter.From == nil && filter.To == nil && filter.MatterID == nil {
		return nil, nil
	}
	return filter, nil
}

// searchMemoriesMatterFirst 两段式检索:第一段事项(最多 2 个,命中即给全景),
// 第二段散点记忆(排除已随事项展示过的,避免重复)。
func searchMemoriesMatterFirst(ctx context.Context, matterSearcher MatterSearcher, recent RecentMessageSearcher, searcher MemorySearcher, userID int64, query string, filter *memorydomain.MemoryFilter) (string, error) {
	matterHits, err := matterSearcher.SearchMatters(ctx, userID, query, 2)
	if err != nil {
		return "", fmt.Errorf("查询记忆失败: %w", err)
	}

	seen := make(map[int64]bool)
	var b strings.Builder
	for _, mh := range matterHits {
		// 事项带最近更新时间:LLM 能判断状态的新旧
		fmt.Fprintf(&b, "【事项】%s(更新于 %s)\n当前状态:%s\n",
			mh.Matter.Title, mh.Matter.UpdatedAt.Format("2006-01-02"), mh.Matter.StateDesc)
		for _, f := range mh.Facts {
			seen[f.ID] = true
			fmt.Fprintf(&b, "- %s(%s · %s)\n", f.Content, f.Kind, f.CreatedAt.Format("2006-01-02"))
		}
		b.WriteString("\n")
	}

	// 中期区(M7):近期对话原文,事项区与散点区之间
	writeRecentSection(ctx, recent, userID, query, filter, &b)

	hits, err := searcher.SearchMemories(ctx, userID, query, 10, filter)
	if err != nil {
		return "", fmt.Errorf("查询记忆失败: %w", err)
	}
	if filter != nil && (filter.From != nil || filter.To != nil) {
		// 22号:时间线输出——按有效时间升序重排("这段时间做了什么"的阅读顺序)
		sort.Slice(hits, func(i, j int) bool { return effectiveTimeOf(hits[i].Memory).Before(effectiveTimeOf(hits[j].Memory)) })
	}
	shown := 0
	for _, h := range hits {
		if seen[h.Memory.ID] {
			continue
		}
		shown++
		fmt.Fprintf(&b, "%d. %s%s\n", shown, h.Memory.Content, memoryAnnotation(h.Memory))
	}

	if len(matterHits) == 0 && shown == 0 {
		return "未找到相关记忆", nil
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// safeEval 安全的数学表达式求值（仅允许数字、运算符、括号、空格和小数点）

func filterMemories(memories []string, query string) string {
	query = strings.ToLower(query)
	var matched []string
	for _, m := range memories {
		if strings.Contains(strings.ToLower(m), query) {
			matched = append(matched, m)
		}
	}
	if len(matched) == 0 {
		return fmt.Sprintf("未找到与 \"%s\" 相关的记忆", query)
	}
	return strings.Join(matched, "\n")
}

// CreateRSSReaderTool RSS订阅阅读工具，支持解析所有主流RSS/Atom格式
