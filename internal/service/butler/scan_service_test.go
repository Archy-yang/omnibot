package butler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	butlerdomain "omnibot/internal/domain/butler"
	memorydomain "omnibot/internal/domain/memory"
	userdomain "omnibot/internal/domain/user"
)

// ---------- stubs ----------

type stubMatters struct{ list []*memorydomain.Matter }

func (s *stubMatters) ListActiveByUserID(int64) ([]*memorydomain.Matter, error) { return s.list, nil }

type stubMemoriesReal struct{ loops, journal []*memorydomain.Memory }

func (s *stubMemoriesReal) ListOpenLoops(_ int64, _ int) ([]*memorydomain.Memory, error) {
	return s.loops, nil
}
func (s *stubMemoriesReal) ListJournal(_ int64, _ []string, from, to, _ *time.Time, _ int64, _ int) ([]*memorydomain.Memory, error) {
	return s.journal, nil
}

type stubLLM struct {
	called bool
	system string
	user   string
	out    string
	err    error
}

func (s *stubLLM) Complete(_ context.Context, _ int64, system, user string) (string, error) {
	s.called = true
	s.system = system
	s.user = user
	return s.out, s.err
}

type stubUsers struct{ u *userdomain.User }

func (s *stubUsers) GetByID(int64) (*userdomain.User, error) { return s.u, nil }

type memNoteRepo struct{ notes []*butlerdomain.ProactiveNote }

func (m *memNoteRepo) Create(n *butlerdomain.ProactiveNote) error {
	n.ID = int64(len(m.notes) + 1)
	m.notes = append(m.notes, n)
	return nil
}
func (m *memNoteRepo) CountDeliveredOnDay(_ int64, dayStart time.Time) (int64, error) {
	var n int64
	for _, note := range m.notes {
		if note.Status == butlerdomain.NoteStatusDelivered &&
			!note.CreatedAt.Before(dayStart) && note.CreatedAt.Before(dayStart.Add(24*time.Hour)) {
			n++
		}
	}
	return n, nil
}
func (m *memNoteRepo) HasDeliveredSince(_ int64, dedupKey string, since time.Time) (bool, error) {
	for _, note := range m.notes {
		if note.DedupKey == dedupKey && note.Status == butlerdomain.NoteStatusDelivered && !note.CreatedAt.Before(since) {
			return true, nil
		}
	}
	return false, nil
}
func (m *memNoteRepo) ListRecent(_ int64, limit int) ([]*butlerdomain.ProactiveNote, error) {
	if limit <= 0 || limit > len(m.notes) {
		limit = len(m.notes)
	}
	return m.notes[:limit], nil
}

// ---------- fixtures ----------

// scanTime:2026-10-16 09:30(默认 hour=9 的用户到点时刻,且在夜间窗 22:00-08:00 之外)
func scanTime() time.Time { return time.Date(2026, 10, 16, 9, 30, 0, 0, time.Local) }

func enabledUser(hour int) *userdomain.User {
	return &userdomain.User{ID: 1, ProactiveEnabled: true, ProactiveHour: hour}
}

func newTestService(matters *stubMatters, mems *stubMemoriesReal, llm *stubLLM, notes *memNoteRepo, u *userdomain.User) *ScanService {
	return NewScanService(notes, matters, mems, &stubUsers{u: u}, llm)
}

const sendFalseJSON = `{"send": false, "reason": "近期无事值得主动提"}`

const sendTrueJSON = `{"send": true, "kind": "matter_due", "dedup_key": "matter:42", "content": "你上周提的项目方案今天下午该交了,需要我帮你再过一遍要点吗?"}`

// ---------- tests ----------

func TestRunOnce_EnabledFalse_FullNoop(t *testing.T) {
	llm := &stubLLM{out: sendTrueJSON}
	notes := &memNoteRepo{}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案"}}},
		&stubMemoriesReal{}, llm, notes, &userdomain.User{ID: 1, ProactiveEnabled: false, ProactiveHour: 9})

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	assert.Nil(t, note)
	assert.False(t, llm.called, "关闭时不应调 LLM")
	assert.Empty(t, notes.notes, "关闭时不应落库")
}

func TestRunOnce_HourMismatch_Noop(t *testing.T) {
	llm := &stubLLM{out: sendTrueJSON}
	notes := &memNoteRepo{}
	svc := newTestService(&stubMatters{}, &stubMemoriesReal{}, llm, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, time.Date(2026, 10, 16, 10, 30, 0, 0, time.Local))
	require.NoError(t, err)
	assert.Nil(t, note)
	assert.False(t, llm.called, "hour 错位不触发")
}

func TestRunOnce_NightWindow_Rejected(t *testing.T) {
	// 用户配置 23 点:配置本身合法,但 22:00-08:00 代码层硬拒绝
	for _, h := range []int{23, 3} {
		llm := &stubLLM{out: sendTrueJSON}
		notes := &memNoteRepo{}
		svc := newTestService(&stubMatters{}, &stubMemoriesReal{}, llm, notes, enabledUser(h))
		note, err := svc.RunOnce(context.Background(), 1, time.Date(2026, 10, 16, h, 0, 0, 0, time.Local))
		require.NoError(t, err)
		assert.Nil(t, note)
		assert.False(t, llm.called, "夜间时段(%d 点)应被拒绝", h)
	}
}

func TestRunOnce_EmptySignals_NoLLM(t *testing.T) {
	llm := &stubLLM{out: sendTrueJSON}
	notes := &memNoteRepo{}
	svc := newTestService(&stubMatters{}, &stubMemoriesReal{}, llm, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	assert.Nil(t, note)
	assert.False(t, llm.called, "空快照不应调 LLM")
}

func TestRunOnce_SendFalse_NoRows(t *testing.T) {
	llm := &stubLLM{out: sendFalseJSON}
	notes := &memNoteRepo{}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案", StateDesc: "周五前交"}}},
		&stubMemoriesReal{}, llm, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	assert.Nil(t, note)
	assert.True(t, llm.called)
	assert.Empty(t, notes.notes, "send:false 零落库")
}

func TestRunOnce_SendTrue_DeliveredRow(t *testing.T) {
	matter := &memorydomain.Matter{ID: 42, Title: "项目方案", StateDesc: "周五前交"}
	loop := &memorydomain.Memory{ID: 101, Kind: "loop", LoopStatus: "open", Content: "答应给朋友的旅行攻略", CreatedAt: scanTime().Add(-5 * 24 * time.Hour)}
	mem := &memorydomain.Memory{ID: 201, Kind: "fact", Content: "下周一开始健身计划", HappenedAt: ptrTime(scanTime().Add(-24 * time.Hour))}
	llm := &stubLLM{out: sendTrueJSON}
	notes := &memNoteRepo{}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{matter}},
		&stubMemoriesReal{loops: []*memorydomain.Memory{loop}, journal: []*memorydomain.Memory{mem}}, llm, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	require.NotNil(t, note)
	assert.Equal(t, butlerdomain.NoteStatusDelivered, note.Status)
	assert.Equal(t, "matter_due", note.Kind)
	assert.Equal(t, "matter:42", note.DedupKey)
	assert.Contains(t, note.Content, "项目方案")
	assert.NotNil(t, note.DeliveredAt)
	assert.NotNil(t, note.MatterID, "matter_due 溯源事项")
	assert.Equal(t, int64(42), *note.MatterID)

	// 信号快照:三类信号都进了 user 提示词,system 带克制纪律
	assert.Contains(t, llm.user, "matter:42")
	assert.Contains(t, llm.user, "项目方案")
	assert.Contains(t, llm.user, "loop:101")
	assert.Contains(t, llm.user, "旅行攻略")
	assert.Contains(t, llm.user, "健身计划")
	assert.Contains(t, llm.system, "宁可漏")
}

func TestRunOnce_DailyLimit_GatesBeforeLLM(t *testing.T) {
	llm := &stubLLM{out: sendTrueJSON}
	notes := &memNoteRepo{notes: []*butlerdomain.ProactiveNote{{
		UserID: 1, Kind: butlerdomain.NoteKindLoopStale, Content: "早上的那条",
		DedupKey: "loop:99", Status: butlerdomain.NoteStatusDelivered,
		CreatedAt: scanTime(), DeliveredAt: ptrTime(scanTime()),
	}}}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案"}}},
		&stubMemoriesReal{}, llm, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	assert.Nil(t, note)
	assert.False(t, llm.called, "代码闸门优先于 LLM:当日已发不再调 LLM")
}

func TestRunOnce_DedupCooldown_SkippedRow(t *testing.T) {
	twoDaysAgo := scanTime().Add(-48 * time.Hour)
	llm := &stubLLM{out: sendTrueJSON}
	notes := &memNoteRepo{notes: []*butlerdomain.ProactiveNote{{
		UserID: 1, Kind: butlerdomain.NoteKindMatterDue, Content: "两天前提过",
		DedupKey: "matter:42", Status: butlerdomain.NoteStatusDelivered,
		CreatedAt: twoDaysAgo, DeliveredAt: ptrTime(twoDaysAgo),
	}}}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案", StateDesc: "周五前交"}}},
		&stubMemoriesReal{}, llm, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	require.NotNil(t, note)
	assert.Equal(t, butlerdomain.NoteStatusSkipped, note.Status, "冷却期内应拦下并记 skipped")
	assert.Equal(t, "matter:42", note.DedupKey)
	assert.Nil(t, note.DeliveredAt, "skipped 未送达")
	// 库里 delivered 仍只有两天前那一条
	var delivered int
	for _, n := range notes.notes {
		if n.Status == butlerdomain.NoteStatusDelivered {
			delivered++
		}
	}
	assert.Equal(t, 1, delivered)
}

func TestRunOnce_DedupKeyOutsideCooldown_Delivered(t *testing.T) {
	eightDaysAgo := scanTime().Add(-8 * 24 * time.Hour)
	llm := &stubLLM{out: sendTrueJSON}
	notes := &memNoteRepo{notes: []*butlerdomain.ProactiveNote{{
		UserID: 1, DedupKey: "matter:42", Status: butlerdomain.NoteStatusDelivered,
		CreatedAt: eightDaysAgo, DeliveredAt: ptrTime(eightDaysAgo),
	}}}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案", StateDesc: "周五前交"}}},
		&stubMemoriesReal{}, llm, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	require.NotNil(t, note)
	assert.Equal(t, butlerdomain.NoteStatusDelivered, note.Status, "超过 7 天冷却窗口应放行")
}

func TestRunOnce_InvalidJSON_Noop(t *testing.T) {
	for _, out := range []string{"我觉得没什么好说的", `{"send": true, "dedup_key": ""}`, ""} {
		llm := &stubLLM{out: out}
		notes := &memNoteRepo{}
		svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案"}}},
			&stubMemoriesReal{}, llm, notes, enabledUser(9))
		note, err := svc.RunOnce(context.Background(), 1, scanTime())
		require.NoError(t, err)
		assert.Nil(t, note, "无效输出(%q)应静默 no-op", out)
		assert.Empty(t, notes.notes)
	}
}

func TestRunOnce_JSONFences_Tolerated(t *testing.T) {
	wrapped := "```json\n" + sendTrueJSON + "\n```"
	llm := &stubLLM{out: wrapped}
	notes := &memNoteRepo{}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案", StateDesc: "周五前交"}}},
		&stubMemoriesReal{}, llm, notes, enabledUser(9))

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	require.NotNil(t, note)
	assert.Equal(t, butlerdomain.NoteStatusDelivered, note.Status)
}

func TestRunOnce_LoopStale_FilterThreeDays(t *testing.T) {
	fresh := &memorydomain.Memory{ID: 1, Kind: "loop", Content: "刚开的 loop", CreatedAt: scanTime().Add(-24 * time.Hour)}
	stale := &memorydomain.Memory{ID: 101, Kind: "loop", Content: "搁置已久的 loop", CreatedAt: scanTime().Add(-5 * 24 * time.Hour)}
	llm := &stubLLM{out: sendFalseJSON}
	svc := newTestService(&stubMatters{}, &stubMemoriesReal{loops: []*memorydomain.Memory{fresh, stale}}, llm, &memNoteRepo{}, enabledUser(9))

	_, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	assert.Contains(t, llm.user, "loop:101")
	assert.NotContains(t, llm.user, "[loop:1] ", "open 未满 3 天的 loop 不进信号")
}

func ptrTime(t time.Time) *time.Time { return &t }
