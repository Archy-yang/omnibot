package butler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	butlerdomain "omnibot/internal/domain/butler"
	userdomain "omnibot/internal/domain/user"
)

// updatableUserStore 内存实现 GetByID+Update(设置服务窄接口)。
type updatableUserStore struct{ users map[int64]*userdomain.User }

func (s *updatableUserStore) GetByID(id int64) (*userdomain.User, error) {
	u, ok := s.users[id]
	if !ok {
		u = &userdomain.User{ID: id, ProactiveEnabled: false, ProactiveHour: 9}
		s.users[id] = u
	}
	return u, nil
}
func (s *updatableUserStore) Update(u *userdomain.User) error {
	s.users[u.ID] = u
	return nil
}
func (s *updatableUserStore) ListProactiveEnabled() ([]*userdomain.User, error) { return nil, nil }

func newSettingsService() (*SettingsService, *updatableUserStore, *memNoteRepo) {
	users := &updatableUserStore{users: make(map[int64]*userdomain.User)}
	notes := &memNoteRepo{}
	return NewSettingsService(users, notes), users, notes
}

func TestSettingsService_DefaultsOff(t *testing.T) {
	svc, _, _ := newSettingsService()
	enabled, hour, err := svc.GetSettings(1)
	require.NoError(t, err)
	assert.False(t, enabled, "默认关闭(红线)")
	assert.Equal(t, 9, hour, "默认 9 点")
}

func TestSettingsService_Update(t *testing.T) {
	svc, users, _ := newSettingsService()

	require.NoError(t, svc.UpdateSettings(1, true, 10))
	enabled, hour, err := svc.GetSettings(1)
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.Equal(t, 10, hour)
	assert.True(t, users.users[1].ProactiveEnabled)

	// 关闭:立即静默(只是开关翻转,无残留)
	require.NoError(t, svc.UpdateSettings(1, false, 10))
	enabled, _, err = svc.GetSettings(1)
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestSettingsService_UpdateHourValidation(t *testing.T) {
	svc, _, _ := newSettingsService()
	for _, h := range []int{-1, 24, 99} {
		require.Error(t, svc.UpdateSettings(1, true, h), "hour=%d 应拒绝", h)
	}
	// 0-23 全部合法(夜间时段配置合法,触发层硬拒绝)
	for _, h := range []int{0, 8, 9, 22, 23} {
		require.NoError(t, svc.UpdateSettings(1, true, h))
	}
}

func TestSettingsService_ListNotes(t *testing.T) {
	svc, _, notes := newSettingsService()
	require.NoError(t, notes.Create(&butlerdomain.ProactiveNote{
		UserID: 1, Kind: butlerdomain.NoteKindMatterDue, Content: "项目方案今天下午该交了",
		DedupKey: "matter:42", Status: butlerdomain.NoteStatusDelivered,
	}))
	list, err := svc.ListNotes(1, 20)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "matter:42", list[0].DedupKey)
}
