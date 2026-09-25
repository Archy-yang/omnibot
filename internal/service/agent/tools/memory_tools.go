package tools

// memory_tools.go — 记忆检索工具(12-记忆系统 §8;自 agent 包迁入)。
// MemoryProvider/MemorySearcher/MatterSearcher/RecentMessageSearcher 接口随迁,
// agent 核心不感知记忆实现。

import (
	"context"
	"fmt"
	"strings"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/internal/pkg/toolcore"
)

type MemoryProvider interface {
	GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
}

// MemorySearcher 语义记忆检索(可选增强,12-记忆系统技术方案 §8)。
// MemoryProvider 实现可额外实现此接口;search_memories 工具优先走语义路径,否则老子串兜底。
type MemorySearcher interface {
	SearchMemories(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MemoryHit, error)
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
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			query, ok := args["query"].(string)
			if !ok || query == "" {
				return "", fmt.Errorf("query is required")
			}
			userID := toolcore.UserIDFromContext(ctx)
			if searcher, ok := memorySvc.(MemorySearcher); ok {
				recent, _ := memorySvc.(RecentMessageSearcher) // M7:中期区可选,无实现则省略
				// M7 三段式:事项命中优先(返回"这件事"的全景) → 近期对话原文 → 散点记忆
				if matterSearcher, ok := memorySvc.(MatterSearcher); ok {
					return searchMemoriesMatterFirst(ctx, matterSearcher, recent, searcher, userID, query)
				}
				return searchMemoriesSemantic(ctx, recent, searcher, userID, query)
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
	SearchRecentMessages(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MessageHit, error)
}

// searchMemoriesSemantic 语义检索路径:近期对话原文(可选)+ 记忆内容 + 来源/时间标注。
func searchMemoriesSemantic(ctx context.Context, recent RecentMessageSearcher, searcher MemorySearcher, userID int64, query string) (string, error) {
	hits, err := searcher.SearchMemories(ctx, userID, query, 10)
	if err != nil {
		return "", fmt.Errorf("查询记忆失败: %w", err)
	}
	var b strings.Builder
	writeRecentSection(ctx, recent, userID, query, &b)
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
func writeRecentSection(ctx context.Context, recent RecentMessageSearcher, userID int64, query string, b *strings.Builder) {
	if recent == nil {
		return
	}
	msgHits, err := recent.SearchRecentMessages(ctx, userID, query, recentSearchTopK)
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

// memoryAnnotation 记忆条目的括号标注:来源(自动/手动)+ 发生日期(记录时间),
// 让 LLM 回答时能带上"什么时候"而不只"是什么"。
func memoryAnnotation(m *memorydomain.Memory) string {
	parts := make([]string, 0, 2)
	if m.Source == memorydomain.MemorySourceAuto {
		parts = append(parts, "自动记忆")
	}
	if !m.CreatedAt.IsZero() {
		parts = append(parts, m.CreatedAt.Format("2006-01-02"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " · ") + ")"
}

// searchMemoriesMatterFirst 两段式检索:第一段事项(最多 2 个,命中即给全景),
// 第二段散点记忆(排除已随事项展示过的,避免重复)。
func searchMemoriesMatterFirst(ctx context.Context, matterSearcher MatterSearcher, recent RecentMessageSearcher, searcher MemorySearcher, userID int64, query string) (string, error) {
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
	writeRecentSection(ctx, recent, userID, query, &b)

	hits, err := searcher.SearchMemories(ctx, userID, query, 10)
	if err != nil {
		return "", fmt.Errorf("查询记忆失败: %w", err)
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
