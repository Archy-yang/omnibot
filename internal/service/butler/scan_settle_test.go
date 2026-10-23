package butler

// 28号 N1 完结裁决测试:close_suggest 输出解析 + dedup_key 后缀归属解析 + 信号行最后动静/目标紧迫度。

import (
	"context"
	"testing"
	"time"

	butlerdomain "omnibot/internal/domain/butler"
	memorydomain "omnibot/internal/domain/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dedup_key "matter:10:close" 的 :close 后缀不影响事项 ID 归属解析。
func TestMatterIDFromKey_CloseSuffix(t *testing.T) {
	id, ok := matterIDFromKey("matter:10:close")
	require.True(t, ok)
	assert.Equal(t, int64(10), id)

	// 兼容无后缀既有形态
	id, ok = matterIDFromKey("matter:42")
	require.True(t, ok)
	assert.Equal(t, int64(42), id)

	// 非 matter 键不误判
	_, ok = matterIDFromKey("loop:34:close")
	assert.False(t, ok)
}

// close_suggest 裁决输出解析:kind 保留、dedup/content 齐全即开口。
func TestParseDecision_CloseSuggest(t *testing.T) {
	raw := `{"send": true, "kind": "close_suggest", "dedup_key": "matter:10:close",
		"matter_id": 10, "content": "父亲的诊治看起来已经有结果了,要我把它收掉吗?"}`
	d := parseDecision(raw)
	require.NotNil(t, d)
	assert.Equal(t, butlerdomain.NoteKindCloseSuggest, d.Kind)
	assert.Equal(t, "matter:10:close", d.DedupKey)
	require.NotNil(t, d.MatterID)
	assert.Equal(t, int64(10), *d.MatterID)
}

// matter 信号行带最后动静(闲置时长是裁决"该收了吗"的核心证据)。
func TestBuildSnapshot_MatterLastActivity(t *testing.T) {
	matters := &stubMatters{list: []*memorydomain.Matter{{
		ID: 10, Title: "父亲的诊治", StateDesc: "体温已回落", UpdatedAt: scanTime().AddDate(0, 0, -4),
	}}}
	svc := newTestService(matters, &stubMemoriesReal{}, &stubLLM{}, &memNoteRepo{}, enabledUser(9))

	snapshot, err := svc.buildSnapshot(1, scanTime())
	require.NoError(t, err)
	assert.Contains(t, snapshot, "[matter:10] 父亲的诊治")
	assert.Contains(t, snapshot, "最后动静")
	assert.Contains(t, snapshot, "4 天", "距今天数要可读")
}

// matter 带 target_at 时信号行带目标日与剩余天数(28号 N2 信号面)。
func TestBuildSnapshot_MatterWithTarget(t *testing.T) {
	matters := &stubMatters{list: []*memorydomain.Matter{{
		ID: 10, Title: "完成体检", TargetAt: ptrTime(scanTime().AddDate(0, 0, 9)),
	}}}
	svc := newTestService(matters, &stubMemoriesReal{}, &stubLLM{}, &memNoteRepo{}, enabledUser(9))

	snapshot, err := svc.buildSnapshot(1, scanTime())
	require.NoError(t, err)
	assert.Contains(t, snapshot, "目标")
	assert.Contains(t, snapshot, "剩 9 天")
}

// 29号 N2:goal_nudge 输出解析——推进建议与收口建议同构(dedup_key :goal 后缀)。
func TestMatterIDFromKey_GoalSuffix(t *testing.T) {
	id, ok := matterIDFromKey("matter:10:goal")
	require.True(t, ok)
	assert.Equal(t, int64(10), id)

	_, ok = matterIDFromKey("loop:34:goal")
	assert.False(t, ok, "非 matter 键不误判")
}

func TestParseDecision_GoalNudge(t *testing.T) {
	raw := `{"send": true, "kind": "goal_nudge", "dedup_key": "matter:10:goal",
		"matter_id": 10, "content": "体检还剩 2 天没动静,要我派个活去查附近的体检机构吗?"}`
	d := parseDecision(raw)
	require.NotNil(t, d)
	assert.Equal(t, butlerdomain.NoteKindGoalNudge, d.Kind)
	assert.Equal(t, "matter:10:goal", d.DedupKey)
	require.NotNil(t, d.MatterID)
	assert.Equal(t, int64(10), *d.MatterID)
}

// 裁决 prompt 必须覆盖 goal_nudge 的触发条件与红线(防后续改 prompt 静默丢失指令)。
func TestAdjudicationPrompt_GoalNudgeInstructions(t *testing.T) {
	assert.Contains(t, adjudicationSystemPrompt, "goal_nudge")
	assert.Contains(t, adjudicationSystemPrompt, ":goal", "dedup_key 后缀约定要写进 prompt")
	assert.Contains(t, adjudicationSystemPrompt, "具体可执行", "推进建议禁空泛的红线要写进 prompt")
}

// 29号 N3:该 dedup_key 最近一条 delivered 反馈为 negative → 冷却窗 7 天 → 30 天;
// positive 不改变窗口(负反馈优先,防正反馈通胀)。
func TestRunOnce_NegativeFeedback_ExtendsCooldown(t *testing.T) {
	tenDaysAgo := scanTime().Add(-10 * 24 * time.Hour)
	notes := &memNoteRepo{notes: []*butlerdomain.ProactiveNote{{
		UserID: 1, Kind: butlerdomain.NoteKindMatterDue, Content: "十天前提过,用户点了没用",
		DedupKey: "matter:42", Status: butlerdomain.NoteStatusDelivered,
		Feedback:  butlerdomain.NoteFeedbackNegative,
		CreatedAt: tenDaysAgo, DeliveredAt: ptrTime(tenDaysAgo),
	}}}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案", StateDesc: "周五前交"}}},
		&stubMemoriesReal{}, &stubLLM{out: sendTrueJSON}, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	require.NotNil(t, note)
	assert.Equal(t, butlerdomain.NoteStatusSkipped, note.Status, "negative 冷却 30 天:10 天前同键开口应拦下(7 天窗已放行)")
}

func TestRunOnce_PositiveFeedback_DoesNotExtendCooldown(t *testing.T) {
	tenDaysAgo := scanTime().Add(-10 * 24 * time.Hour)
	notes := &memNoteRepo{notes: []*butlerdomain.ProactiveNote{{
		UserID: 1, Kind: butlerdomain.NoteKindMatterDue, Content: "十天前提过,用户说有用",
		DedupKey: "matter:42", Status: butlerdomain.NoteStatusDelivered,
		Feedback:  butlerdomain.NoteFeedbackPositive,
		CreatedAt: tenDaysAgo, DeliveredAt: ptrTime(tenDaysAgo),
	}}}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案", StateDesc: "周五前交"}}},
		&stubMemoriesReal{}, &stubLLM{out: sendTrueJSON}, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	require.NotNil(t, note)
	assert.Equal(t, butlerdomain.NoteStatusDelivered, note.Status, "positive 不延长冷却窗:10 天前 > 7 天应放行")
}
