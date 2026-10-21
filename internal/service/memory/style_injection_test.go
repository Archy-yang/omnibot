package memory

// 风格注入(M2,24号 §6 修订版):开关开启时,把最近的原始风格观察拼成注入段,
// 由 chat 层 BuildContextMessages 作为 system 消息注入(与记忆常驻块同通道)。
// 红线(20号 §8):只注原始观察、无 LLM 聚合;开关默认关;关 = 完全不注入。

import (
	"context"
	"errors"
	"strings"
	"testing"

	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fakeStyleStore 注入开关窄接口的测试桩。
type fakeStyleStore struct {
	enabled bool
	err     error
}

func (f *fakeStyleStore) GetStyleInjectionEnabled(int64) (bool, error) {
	return f.enabled, f.err
}

func (f *fakeStyleStore) SetStyleInjectionEnabled(_ int64, enabled bool) error {
	if f.err != nil {
		return f.err
	}
	f.enabled = enabled
	return nil
}

func styleServiceSetup(t *testing.T) *memoryService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&conversation.Message{}, &memorydomain.StyleObservation{}, &memorydomain.CognitionChange{}))
	return &memoryService{repo: memoryrepo.NewMemoryRepository(db)}
}

func seedObservations(t *testing.T, s *memoryService, userID int64, n int, content string) {
	t.Helper()
	rows := make([]memorydomain.StyleObservation, n)
	for i := range rows {
		rows[i] = memorydomain.StyleObservation{UserID: userID, Dimension: "tone", Content: content}
	}
	require.NoError(t, s.repo.CreateStyleObservations(rows))
}

func TestStylePromptSegment_Disabled(t *testing.T) {
	s := styleServiceSetup(t)
	s.styleStore = &fakeStyleStore{enabled: false}
	seedObservations(t, s, 42, 3, "用户偏好直接、少客套")

	assert.Empty(t, s.StylePromptSegment(context.Background(), 42), "开关关 = 完全不注入(20号红线)")
}

func TestStylePromptSegment_NilStore(t *testing.T) {
	s := styleServiceSetup(t)
	assert.Empty(t, s.StylePromptSegment(context.Background(), 42), "未装配开关存取 = 未启用 M2,不注入")
}

func TestStylePromptSegment_EnabledNoObservations(t *testing.T) {
	s := styleServiceSetup(t)
	s.styleStore = &fakeStyleStore{enabled: true}

	assert.Empty(t, s.StylePromptSegment(context.Background(), 42), "开启但无观察 → 不追加空段")
}

func TestStylePromptSegment_EnabledWithObservations(t *testing.T) {
	s := styleServiceSetup(t)
	s.styleStore = &fakeStyleStore{enabled: true}
	rows := []memorydomain.StyleObservation{
		{UserID: 42, Dimension: "tone", Content: "说话随性口语"},
		{UserID: 42, Dimension: "length", Content: "指令极简短,直奔操作"},
	}
	require.NoError(t, s.repo.CreateStyleObservations(rows))

	seg := s.StylePromptSegment(context.Background(), 42)
	assert.Contains(t, seg, "沟通风格")
	assert.Contains(t, seg, "自然参考", "必须带克制提示,不要机械执行")
	assert.Contains(t, seg, "- [tone] 说话随性口语")
	assert.Contains(t, seg, "- [length] 指令极简短,直奔操作")
}

func TestStylePromptSegment_StoreErrorDegrades(t *testing.T) {
	s := styleServiceSetup(t)
	s.styleStore = &fakeStyleStore{enabled: true, err: errors.New("db down")}
	seedObservations(t, s, 42, 3, "x")

	assert.Empty(t, s.StylePromptSegment(context.Background(), 42), "开关读取失败静默降级,不阻塞对话")
}

func TestStylePromptSegment_ObservationCap(t *testing.T) {
	s := styleServiceSetup(t)
	s.styleStore = &fakeStyleStore{enabled: true}
	seedObservations(t, s, 42, 12, "短观察")

	seg := s.StylePromptSegment(context.Background(), 42)
	assert.Equal(t, 10, strings.Count(seg, "- ["), "最多注入 10 条观察")
}

func TestStylePromptSegment_ItemTruncated(t *testing.T) {
	s := styleServiceSetup(t)
	s.styleStore = &fakeStyleStore{enabled: true}
	seedObservations(t, s, 42, 1, strings.Repeat("长", 300))

	seg := s.StylePromptSegment(context.Background(), 42)
	assert.Less(t, len([]rune(seg)), 200, "单条观察截断,不整段塞入 300 字")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(seg), "…"), "截断以省略号收尾")
}

func TestSetStyleInjection(t *testing.T) {
	s := styleServiceSetup(t)
	store := &fakeStyleStore{}
	s.styleStore = store

	require.NoError(t, s.SetStyleInjection(42, true))
	assert.True(t, store.enabled)

	require.NoError(t, s.SetStyleInjection(42, false))
	assert.False(t, store.enabled)
}
