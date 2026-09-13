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
