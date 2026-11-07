package agent

// llm_diag.go — LLM 轮次诊断埋点(2026-11-04)。
//
// 背景:用户反馈"回复说已安排,但没有任何任务落库"。已确认该轮只有 1 次 LLM 调用、
// 0 次工具调用,但无法从数据区分三种可能:
//   1) 工具根本没发给模型(装配/过滤问题)
//   2) 模型看了工具但没调(模型行为问题)
//   3) 模型调了但流式解析丢了(适配层 bug)
//
// 这里只打**结构化事实**,不做任何文本语义判断(关键词匹配不可靠,已放弃):
//   - 发给模型的工具清单与数量
//   - 每轮收尾的 finish_reason + 工具调用数 + delegate 执行数
//
// 有了这三项,复现一次就能定位到上面哪一层。

import (
	"sort"

	"go.uber.org/zap"

	"omnibot/pkg/logger"
)

// logLLMTools 记录本轮发给模型的工具清单(排除"工具没发出去")。
// 只打名字与数量,不打 schema/参数,避免日志膨胀。
func logLLMTools(tools []map[string]interface{}) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		fn, ok := t["function"].(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := fn["name"].(string); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	logger.InfoWithFields("agent: llm tools attached",
		zap.Int("count", len(names)), zap.Strings("tools", names))
}

// logRoundWithoutTools 记录"本轮零工具调用"的收尾事实。
//
// finish_reason 是判定关键:
//   - "stop" 且 tool_calls=0  → 模型自己选择不调(第 2 类)
//   - "tool_calls" 且 tool_calls=0 → 解析/适配层丢了(第 3 类,bug)
//   - 工具清单里没有 delegate → 第 1 类(装配问题)
func logRoundWithoutTools(step int, finishReason, content string, delegateCalls int) {
	logger.InfoWithFields("agent: llm round finished without tool calls",
		zap.Int("step", step),
		zap.String("finish_reason", finishReason),
		zap.Int("content_runes", len([]rune(content))),
		zap.Int("delegate_calls", delegateCalls))
}
