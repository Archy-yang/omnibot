package memory

import (
	"strings"
	"testing"
)

// prompt 守护测试(M5.4):summary 必须是"状态快照"式,关键约束丢失即报错
// (防后续调优把禁过程叙事的约束改丢,流水账复述回归)。
func TestPipelinePrompt_SnapshotConstraints(t *testing.T) {
	for _, want := range []string{
		"状态快照", "禁止复述对话过程", "【关键决定与事实】", "【用户偏好】", "【未决事项】",
		"source_message_ids", "宁可漏记", "只输出 JSON",
	} {
		if !strings.Contains(pipelineSystemPrompt, want) {
			t.Errorf("沉淀 prompt 缺少关键约束 %q", want)
		}
	}
	// 旧版流水账措辞不应回归
	for _, banned := range []string{"概括聊了什么主题", "把这段对话压缩成一段纪要"} {
		if strings.Contains(pipelineSystemPrompt, banned) {
			t.Errorf("沉淀 prompt 回归了旧版流水账措辞 %q", banned)
		}
	}
}
