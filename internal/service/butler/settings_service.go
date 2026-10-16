package butler

// settings_service.go — 主动管家设置(26 号 §5)。
// 读写 users.proactive_enabled/proactive_hour + 最近主动记录列表。
// 红线:默认关闭;关闭 = 立即静默(只是开关翻转,无残留)。

import (
	"fmt"

	butlerdomain "omnibot/internal/domain/butler"
	userdomain "omnibot/internal/domain/user"
	butlerrepo "omnibot/internal/repository/butler"
)

// UserSettingStore 设置读写窄接口(user.UserRepository 满足)。
type UserSettingStore interface {
	GetByID(id int64) (*userdomain.User, error)
	Update(u *userdomain.User) error
}

// SettingsService 主动管家设置服务。
type SettingsService struct {
	users UserSettingStore
	notes butlerrepo.ProactiveNoteRepository
}

func NewSettingsService(users UserSettingStore, notes butlerrepo.ProactiveNoteRepository) *SettingsService {
	return &SettingsService{users: users, notes: notes}
}

// GetSettings 读开关与时刻。
func (s *SettingsService) GetSettings(userID int64) (enabled bool, hour int, err error) {
	u, err := s.users.GetByID(userID)
	if err != nil {
		return false, 0, fmt.Errorf("读用户 %d: %w", userID, err)
	}
	return u.ProactiveEnabled, u.ProactiveHour, nil
}

// UpdateSettings 写开关与时刻。hour 校验 0-23(夜间时段配置合法,触发层代码硬拒绝)。
func (s *SettingsService) UpdateSettings(userID int64, enabled bool, hour int) error {
	if hour < 0 || hour > 23 {
		return fmt.Errorf("hour 超出范围(0-23): %d", hour)
	}
	u, err := s.users.GetByID(userID)
	if err != nil {
		return fmt.Errorf("读用户 %d: %w", userID, err)
	}
	u.ProactiveEnabled = enabled
	u.ProactiveHour = hour
	return s.users.Update(u)
}

// ListNotes 最近主动记录(设置页列表;limit<=0 默认 20)。
func (s *SettingsService) ListNotes(userID int64, limit int) ([]*butlerdomain.ProactiveNote, error) {
	return s.notes.ListRecent(userID, limit)
}
