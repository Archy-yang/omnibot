package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	memorydomain "omnibot/internal/domain/memory"
)

// 21 号 J1:事件时间抽取(happened_at)——Journal 时间轴的锚。
// created_at 是沉淀时间≠事件时间("上周爬的山"今天沉淀会排错位置);
// 沉淀时 LLM 抽取绝对时间,解析层严格容错:空/垃圾/超界一律 NULL(展示回落 created_at)。

func TestParseHappenedAt(t *testing.T) {
	from := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

	// 合法:日期 / 日期时间 / RFC3339
	d := parseHappenedAt("2026-10-01", from, to)
	require.NotNil(t, d)
	require.Equal(t, "2026-10-01", d.Format("2006-01-02"))

	d = parseHappenedAt("2026-10-05 14:30", from, to)
	require.NotNil(t, d)
	require.Equal(t, 14, d.Hour())

	d = parseHappenedAt("2026-10-06T08:00:00Z", from, to)
	require.NotNil(t, d)

	// 空/垃圾/相对时间 → nil(相对时间由 LLM 换算成绝对时间,解析层不猜)
	require.Nil(t, parseHappenedAt("", from, to))
	require.Nil(t, parseHappenedAt("   ", from, to))
	require.Nil(t, parseHappenedAt("上周三", from, to))
	require.Nil(t, parseHappenedAt("not-a-date", from, to))

	// 超界:未来超过 to+24h / 早于 from-1 年 → nil
	require.Nil(t, parseHappenedAt("2030-01-01", from, to), "防幻觉未来日期")
	require.Nil(t, parseHappenedAt("2020-01-01", from, to), "超出下界")
	// 边界内贴边:to+24h 内允许,from-1y 整点允许
	require.NotNil(t, parseHappenedAt(to.Add(23*time.Hour).Format("2006-01-02 15:04"), from, to))
	require.NotNil(t, parseHappenedAt(from.AddDate(-1, 0, 0).Format("2006-01-02"), from, to))
}

// TestReconcile_FactHappenedAt 对账落库携带事件时间;缺失/超界 → NULL。
func TestReconcile_FactHappenedAt(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[
		{"content":"10/1 爬了慕田峪长城","kind":"fact","happened_at":"2026-10-01","source_message_ids":[1]},
		{"content":"不带时间的经历","kind":"fact","source_message_ids":[2]},
		{"content":"幻觉未来日期","kind":"fact","happened_at":"2030-01-01","source_message_ids":[3]}
	]}`

	require.NoError(t, p.RunOnce(context.Background(), 42))

	var mems []memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	require.Len(t, mems, 3)
	byContent := map[string]memorydomain.Memory{}
	for _, m := range mems {
		byContent[m.Content] = m
	}

	require.NotNil(t, byContent["10/1 爬了慕田峪长城"].HappenedAt)
	require.Equal(t, "2026-10-01", byContent["10/1 爬了慕田峪长城"].HappenedAt.Format("2006-01-02"))
	require.Nil(t, byContent["不带时间的经历"].HappenedAt)
	require.Nil(t, byContent["幻觉未来日期"].HappenedAt, "超界值必须 NULL,不得带病落库")
}

// TestPrompt_IncludesHappenedAtInstruction 提示词必须教会 LLM 抽事件时间并禁止编造。
func TestPrompt_IncludesHappenedAtInstruction(t *testing.T) {
	require.Contains(t, pipelineSystemPrompt, "happened_at")
	require.Contains(t, pipelineSystemPrompt, "不要编造")
}
