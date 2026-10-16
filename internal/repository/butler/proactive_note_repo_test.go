package butler

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/butler"
)

func setupNoteTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.ProactiveNote{}))
	return db
}

func newNote(userID int64, kind, dedupKey, status string, createdAt time.Time) *domain.ProactiveNote {
	note := &domain.ProactiveNote{
		UserID:    userID,
		Kind:      kind,
		Content:   "测试主动消息",
		DedupKey:  dedupKey,
		Status:    status,
		CreatedAt: createdAt,
	}
	if status == domain.NoteStatusDelivered {
		note.DeliveredAt = &createdAt
	}
	return note
}

func TestProactiveNoteRepository_Create(t *testing.T) {
	repo := NewProactiveNoteRepository(setupNoteTestDB(t))
	now := time.Now()
	note := newNote(1, domain.NoteKindMatterDue, "matter:42", domain.NoteStatusDelivered, now)

	require.NoError(t, repo.Create(note))
	assert.NotZero(t, note.ID)
	assert.Equal(t, domain.NoteStatusDelivered, note.Status)
}

func TestProactiveNoteRepository_CountDeliveredOnDay(t *testing.T) {
	repo := NewProactiveNoteRepository(setupNoteTestDB(t))
	dayStart := time.Date(2026, 10, 16, 0, 0, 0, 0, time.Local)
	inDay := dayStart.Add(9 * time.Hour)
	yesterday := dayStart.Add(-24 * time.Hour)

	// 昨天的 delivered 不计当日
	require.NoError(t, repo.Create(newNote(1, domain.NoteKindMatterDue, "matter:1", domain.NoteStatusDelivered, yesterday)))
	n, err := repo.CountDeliveredOnDay(1, dayStart)
	require.NoError(t, err)
	assert.Zero(t, n, "昨日 delivered 不应计入今日日限")

	// 当日 delivered 计 1
	require.NoError(t, repo.Create(newNote(1, domain.NoteKindLoopStale, "loop:1", domain.NoteStatusDelivered, inDay)))
	n, err = repo.CountDeliveredOnDay(1, dayStart)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	// 当日 skipped 不计(克制闸门只看真正开口过的)
	require.NoError(t, repo.Create(newNote(1, domain.NoteKindMatterDue, "matter:2", domain.NoteStatusSkipped, inDay)))
	n, err = repo.CountDeliveredOnDay(1, dayStart)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "skipped 不应计入日限")

	// 用户隔离
	require.NoError(t, repo.Create(newNote(2, domain.NoteKindMatterDue, "matter:3", domain.NoteStatusDelivered, inDay)))
	n, err = repo.CountDeliveredOnDay(1, dayStart)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n, "他人记录不应计入")
}

func TestProactiveNoteRepository_HasDeliveredSince(t *testing.T) {
	repo := NewProactiveNoteRepository(setupNoteTestDB(t))
	now := time.Now()
	coolDownStart := now.Add(-7 * 24 * time.Hour)

	// 冷却窗口内 delivered → true
	require.NoError(t, repo.Create(newNote(1, domain.NoteKindMatterDue, "matter:42", domain.NoteStatusDelivered, now.Add(-48*time.Hour))))
	hit, err := repo.HasDeliveredSince(1, "matter:42", coolDownStart)
	require.NoError(t, err)
	assert.True(t, hit)

	// 同 dedup_key 但 skipped → 不触发冷却(冷却只针对真正开口)
	require.NoError(t, repo.Create(newNote(1, domain.NoteKindMatterDue, "matter:43", domain.NoteStatusSkipped, now.Add(-48*time.Hour))))
	hit, err = repo.HasDeliveredSince(1, "matter:43", coolDownStart)
	require.NoError(t, err)
	assert.False(t, hit, "skipped 不应触发 dedup 冷却")

	// 超过 7 天窗口的 delivered → false
	require.NoError(t, repo.Create(newNote(1, domain.NoteKindMatterDue, "matter:44", domain.NoteStatusDelivered, now.Add(-8*24*time.Hour))))
	hit, err = repo.HasDeliveredSince(1, "matter:44", coolDownStart)
	require.NoError(t, err)
	assert.False(t, hit, "超过 7 天窗口不应触发冷却")

	// 用户隔离
	require.NoError(t, repo.Create(newNote(2, domain.NoteKindMatterDue, "matter:42", domain.NoteStatusDelivered, now)))
	hit, err = repo.HasDeliveredSince(1, "matter:42", coolDownStart)
	require.NoError(t, err)
	assert.True(t, hit) // 用户1自己 48h 前已开口,仍命中
	hit, err = repo.HasDeliveredSince(3, "matter:42", coolDownStart)
	require.NoError(t, err)
	assert.False(t, hit, "他人记录不应影响")
}
