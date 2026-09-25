package chat

import (
	"context"
	"errors"
	"strings"

	"omnibot/internal/client/llm"
)

// CompactLLMClient 压缩器所需的最小 LLM 能力(*llm.Client 天然满足;测试可桩)。
type CompactLLMClient interface {
	ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error)
}

// CompactClientResolver 用户级压缩客户端解析器(与 embeddingResolver 同模式):
// 返回 nil 表示无用户配置,调用方回落 fallback。
type CompactClientResolver func(ctx context.Context, userID int64) (CompactLLMClient, error)

// llmContextCompactor LLM 实现的 History Compact 压缩器(Phase 2,§7.1/§7.2)。
//
// Compact(n+1) = Compact( Compact(n) + 被压缩区间原文 ):
// 输入旧工作集 + 新压缩区间,要求输出合并后的 Conversation Working State。
// 客户端按用户解析(压缩与对话同源,用户自定义配置生效);解析失败/无配置回落系统默认。
type llmContextCompactor struct {
	resolve  CompactClientResolver
	fallback CompactLLMClient
}

// NewLLMContextCompactor 创建 LLM 压缩器。fallback 为系统默认客户端,resolve 可为 nil。
func NewLLMContextCompactor(resolve CompactClientResolver, fallback CompactLLMClient) ContextCompactor {
	return &llmContextCompactor{resolve: resolve, fallback: fallback}
}

// compactSystemPrompt §7.2:Compact 是 Conversation Working State,不是聊天摘要。
// 目标是"保留继续对话所需的信息",而不是"最大程度缩短 token"。
const compactSystemPrompt = `你是私人助理的上下文压缩器。你会收到两部分输入：
【当前工作集】此前累积的对话工作状态（首次压缩时为空）
【新压缩区间】这段对话的原文（user/assistant 逐条，含子任务汇报）

请把新区间并入工作集，输出合并后的 <conversation_state>，包含以下小节（无内容的省略该节）：

<conversation_state>
Current Topics: 当前正在讨论的话题
Current Goals: 当前进行中的目标
Decisions: 已做的决定（关键决定带时间点，如"9/25 定了用方案A"）
Rejected Approaches: 被否决的方案及否决原因
Constraints: 确认过的约束条件
Open Questions: 未决问题
Pending Work: 待办与进行中的工作（谁在做、做到哪）
Task / Artifact References: 提及过的后台任务编号与产物
Current State: 一句话概括当前进行到哪
</conversation_state>

要求：
- 保留未来继续对话所需要的信息，不为缩短 token 牺牲关键细节；
- 旧工作集中已完成的待办、已被新进展覆盖的状态要收敛/移除，避免膨胀；
- 冲突时以新区间（更新）为准；
- 只输出 <conversation_state>...</conversation_state>，不要任何其他文字。`

func (c *llmContextCompactor) Compact(ctx context.Context, userID int64, oldCompact string, rawTexts []string) (string, error) {
	if len(rawTexts) == 0 {
		return oldCompact, nil
	}
	client := c.fallback
	if c.resolve != nil {
		if resolved, rerr := c.resolve(ctx, userID); rerr == nil && resolved != nil {
			client = resolved
		}
	}
	var sb strings.Builder
	sb.WriteString("【当前工作集】\n")
	if strings.TrimSpace(oldCompact) != "" {
		sb.WriteString(oldCompact)
	} else {
		sb.WriteString("（空，首次压缩）")
	}
	sb.WriteString("\n\n【新压缩区间】\n")
	for _, t := range rawTexts {
		sb.WriteString(t)
		sb.WriteString("\n")
	}

	out, err := client.ChatCompletion(ctx, []llm.ChatMessage{
		{Role: "system", Content: compactSystemPrompt},
		{Role: "user", Content: sb.String()},
	})
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", errors.New("compact: LLM 返回为空")
	}
	return out, nil
}
