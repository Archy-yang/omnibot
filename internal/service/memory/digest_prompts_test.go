package memory

import (
	"strings"
	"testing"
)

// prompt 守护测试(M6 对账式):关键约束丢失即报错。
// M5.4 是"状态快照"止血;M6 起是对账式(世界观快照+四路输出),守护约束随之升级。
func TestPipelinePrompt_ReconcileConstraints(t *testing.T) {
	for _, want := range []string{
		"世界观快照", "增量对账", "matter_updates", "facts",
		`"fact"`, `"episode"`, `"loop"`, "覆写", "宁可漏记", "只输出 JSON", "source_message_ids",
		// 助理人语气(M7 期间确认):记自己的笔记,禁"用户"开头
		"严禁以\"用户\"开头", "你怎么称呼对方",
		// facts 三条红线(2026-09-15 事故:新闻快照/任务进度/待核实传闻被抽成记忆,
		// 与事项 state_desc 重复,典型的切片流水账)——关键句丢失即报错
		"三个月后还有效", "一律不进 facts", "不要两头都写", "不记世界本身的未确认状态",
		// matter 准入三条+反例(2026-09-15 事故:天气/财经/AI动态等普世查询主题
		// 被立成长事项,新闻事件被当事项跟进)——关键句丢失即报错
		"事项准入三条", "普世查询主题", "他个人的事", "关键进展必须带时间点",
	} {
		if !strings.Contains(pipelineSystemPrompt, want) {
			t.Errorf("沉淀 prompt 缺少关键约束 %q", want)
		}
	}
	// 旧版措辞不应回归(切片流水账/摘要式)
	for _, banned := range []string{"概括聊了什么主题", "把这段对话压缩成一段纪要", "把这段对话沉淀成"} {
		if strings.Contains(pipelineSystemPrompt, banned) {
			t.Errorf("沉淀 prompt 回归了旧版措辞 %q", banned)
		}
	}
}
