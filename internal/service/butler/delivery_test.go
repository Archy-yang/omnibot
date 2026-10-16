package butler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	butlerdomain "omnibot/internal/domain/butler"
	memorydomain "omnibot/internal/domain/memory"
	userdomain "omnibot/internal/domain/user"
)

// ---------- stubs ----------

type stubNotifier struct {
	called  bool
	userID  int64
	content string
	err     error
}

func (s *stubNotifier) NotifyProactive(_ context.Context, userID int64, content string) error {
	s.called = true
	s.userID = userID
	s.content = content
	return s.err
}

type stubSink struct {
	called  bool
	userID  int64
	content string
	err     error
}

func (s *stubSink) SaveProactiveMessage(_ context.Context, userID int64, content string) error {
	s.called = true
	s.userID = userID
	s.content = content
	return s.err
}

func deliveryFixture(llmOut string) (*ScanService, *stubNotifier, *stubSink, *memNoteRepo, *stubLLM) {
	llm := &stubLLM{out: llmOut}
	notes := &memNoteRepo{}
	svc := newTestService(&stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "项目方案", StateDesc: "周五前交"}}},
		&stubMemoriesReal{}, llm, notes, enabledUser(9))
	notifier := &stubNotifier{}
	sink := &stubSink{}
	svc.SetNotifier(notifier)
	svc.SetMessageSink(sink)
	return svc, notifier, sink, notes, llm
}

// ---------- 送达测试 ----------

func TestRunOnce_Delivered_NotifyAndSink(t *testing.T) {
	svc, notifier, sink, _, _ := deliveryFixture(sendTrueJSON)

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	require.NotNil(t, note)

	require.True(t, sink.called, "delivered 应落消息还原")
	assert.Equal(t, int64(1), sink.userID)
	assert.Equal(t, note.Content, sink.content)
	require.True(t, notifier.called, "delivered 应触发送达")
	assert.Equal(t, int64(1), notifier.userID)
	assert.Equal(t, note.Content, notifier.content)
}

func TestRunOnce_Skipped_NoNotifyNoSink(t *testing.T) {
	svc, notifier, sink, _, llm := deliveryFixture(sendTrueJSON)
	// dedup 冷却中 → skipped
	twoDaysAgo := scanTime().Add(-48 * time.Hour)
	svc.notes = &memNoteRepo{notes: []*butlerdomain.ProactiveNote{{
		UserID: 1, DedupKey: "matter:42", Status: butlerdomain.NoteStatusDelivered,
		CreatedAt: twoDaysAgo, DeliveredAt: ptrTime(twoDaysAgo),
	}}}
	_ = llm

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	require.NotNil(t, note)
	assert.Equal(t, butlerdomain.NoteStatusSkipped, note.Status)
	assert.False(t, sink.called, "skipped 不落消息")
	assert.False(t, notifier.called, "skipped 不送达")
}

func TestRunOnce_SinkFailure_StillNotifies(t *testing.T) {
	svc, notifier, sink, _, _ := deliveryFixture(sendTrueJSON)
	sink.err = errors.New("db down")

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err, "消息落库失败不阻断扫描")
	require.NotNil(t, note)
	assert.True(t, notifier.called, "落库失败仍尝试送达(消息能到用户最重要)")
}

func TestRunOnce_NilDeps_Noop(t *testing.T) {
	llm := &stubLLM{out: sendTrueJSON}
	notes := &memNoteRepo{}
	svc := NewScanService(notes, &stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "x", StateDesc: "y"}}},
		&stubMemoriesReal{}, &stubUsers{u: enabledUser(9)}, llm)
	// 不 Set 任何依赖

	note, err := svc.RunOnce(context.Background(), 1, scanTime())
	require.NoError(t, err)
	require.NotNil(t, note, "nil 依赖仅落库,不 panic")
}

// ---------- 调度器测试 ----------

type stubLister struct{ users []*userdomain.User }

func (s *stubLister) ListProactiveEnabled() ([]*userdomain.User, error) { return s.users, nil }

func TestScheduler_RunsOncePerDay(t *testing.T) {
	llm := &stubLLM{out: sendFalseJSON}
	scan := NewScanService(&memNoteRepo{}, &stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "x", StateDesc: "y"}}},
		&stubMemoriesReal{}, &stubUsers{u: enabledUser(9)}, llm)
	sched := NewScanScheduler(scan, &stubLister{users: []*userdomain.User{{ID: 1, ProactiveEnabled: true, ProactiveHour: 9}}})

	day := time.Date(2026, 10, 16, 9, 5, 0, 0, time.Local)
	ran, err := sched.RunDue(context.Background(), day)
	require.NoError(t, err)
	assert.Equal(t, 1, ran, "对位用户应跑一次")
	assert.True(t, llm.called)
	ran, err = sched.RunDue(context.Background(), day.Add(30*time.Minute))
	require.NoError(t, err)
	assert.Zero(t, ran, "同日第二个 tick 不再跑(当日已跑)")
}

func TestScheduler_HourMismatch_Skips(t *testing.T) {
	llm := &stubLLM{out: sendFalseJSON}
	scan := NewScanService(&memNoteRepo{}, &stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "x", StateDesc: "y"}}},
		&stubMemoriesReal{}, &stubUsers{u: enabledUser(9)}, llm)
	sched := NewScanScheduler(scan, &stubLister{users: []*userdomain.User{{ID: 1, ProactiveEnabled: true, ProactiveHour: 9}}})

	ran, err := sched.RunDue(context.Background(), time.Date(2026, 10, 16, 10, 5, 0, 0, time.Local))
	require.NoError(t, err)
	assert.Zero(t, ran)
	assert.False(t, llm.called, "hour 错位不调 LLM")
	// 错位 tick 不该记"已跑":回到对位时刻仍应执行
	ran, err = sched.RunDue(context.Background(), time.Date(2026, 10, 16, 9, 5, 0, 0, time.Local))
	require.NoError(t, err)
	assert.Equal(t, 1, ran)
}

func TestScheduler_ErrorRetriesNextTick(t *testing.T) {
	llm := &stubLLM{out: sendFalseJSON, err: errors.New("llm down")}
	scan := NewScanService(&memNoteRepo{}, &stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "x", StateDesc: "y"}}},
		&stubMemoriesReal{}, &stubUsers{u: enabledUser(9)}, llm)
	sched := NewScanScheduler(scan, &stubLister{users: []*userdomain.User{{ID: 1, ProactiveEnabled: true, ProactiveHour: 9}}})

	_, _ = sched.RunDue(context.Background(), scanTime())
	// LLM 恢复后同日重试应放行(上次失败不记"已跑")
	llm.err = nil
	llm.called = false
	ran, err := sched.RunDue(context.Background(), scanTime().Add(20*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 1, ran, "失败 tick 不记已跑,下个 tick 重试")
	assert.True(t, llm.called)
}

func TestScheduler_MultipleUsers(t *testing.T) {
	llm := &stubLLM{out: sendFalseJSON}
	users := &stubUsers{u: enabledUser(9)}
	notes := &memNoteRepo{}
	scan := NewScanService(notes, &stubMatters{list: []*memorydomain.Matter{{ID: 42, Title: "x", StateDesc: "y"}}},
		&stubMemoriesReal{}, users, llm)
	sched := NewScanScheduler(scan, &stubLister{users: []*userdomain.User{
		{ID: 1, ProactiveEnabled: true, ProactiveHour: 9},
		{ID: 2, ProactiveEnabled: true, ProactiveHour: 10}, // 本 tick 不对位
	}})

	ran, err := sched.RunDue(context.Background(), scanTime())
	require.NoError(t, err)
	assert.Equal(t, 1, ran, "只有对位用户执行")
}

// ---------- 信号收集回归:近 7 天记忆截断不产生非法 UTF-8 ----------

func TestBuildSnapshot_TruncateSafeUTF8(t *testing.T) {
	// 中文 + emoji 混排,验证按 rune 截断(防 PG 22021 老坑)
	long := ""
	for i := 0; i < 50; i++ {
		long += "记忆内容🌾"
	}
	svc := NewScanService(&memNoteRepo{}, &stubMatters{}, &stubMemoriesReal{
		journal: []*memorydomain.Memory{{ID: 1, Kind: "fact", Content: long}},
	}, &stubUsers{u: enabledUser(9)}, &stubLLM{out: sendFalseJSON})

	snap, err := svc.buildSnapshot(1, scanTime())
	require.NoError(t, err)
	assert.Contains(t, snap, "[recent:1]")
	assert.Less(t, len(snap), len(long), "单条信号应被截断")
}
