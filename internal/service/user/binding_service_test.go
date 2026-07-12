package user

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
	userrepo "omnibot/internal/repository/user"
)

func setupBindingTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.User{}, &domain.UserChannel{}, &domain.FeishuBindCode{})
	require.NoError(t, err)
	return db
}

func newBindingService(t *testing.T, ttl time.Duration) (*BindingService, *gorm.DB, userrepo.FeishuBindCodeRepository) {
	db := setupBindingTestDB(t)
	channelRepo := NewGormUserChannelRepositoryShim(db)
	bindCodeRepo := userrepo.NewFeishuBindCodeRepository(db)
	svc := NewBindingService(channelRepo, bindCodeRepo, ttl)
	return svc, db, bindCodeRepo
}

// ---------- IsFeishuBound ----------

func TestBindingService_IsFeishuBound_NotBound(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)
	bound, err := svc.IsFeishuBound(1)
	require.NoError(t, err)
	assert.False(t, bound)
}

func TestBindingService_IsFeishuBound_Bound(t *testing.T) {
	svc, db, _ := newBindingService(t, 5*time.Minute)
	// 直接写入一条飞书绑定
	ch := domain.NewUserChannel(1, "feishu", "ou_openid_xxx")
	require.NoError(t, db.Create(ch).Error)

	bound, err := svc.IsFeishuBound(1)
	require.NoError(t, err)
	assert.True(t, bound)
}

// ---------- GenerateCode ----------

func TestBindingService_GenerateCode_Success(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	code, expires, err := svc.GenerateCode(1)
	require.NoError(t, err)
	assert.Len(t, code, 6)
	assert.True(t, expires.After(time.Now().Add(4 * time.Minute)))

	// 码应能查回
	bound, err := svc.IsFeishuBound(1)
	require.NoError(t, err)
	assert.False(t, bound)
}

func TestBindingService_GenerateCode_AlreadyBound(t *testing.T) {
	svc, db, _ := newBindingService(t, 5*time.Minute)
	ch := domain.NewUserChannel(1, "feishu", "ou_openid_xxx")
	require.NoError(t, db.Create(ch).Error)

	_, _, err := svc.GenerateCode(1)
	assert.ErrorIs(t, err, ErrAccountAlreadyBound)
}

func TestBindingService_GenerateCode_RegenerateInvalidatesOld(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	code1, _, err := svc.GenerateCode(1)
	require.NoError(t, err)
	code2, _, err := svc.GenerateCode(1)
	require.NoError(t, err)
	assert.NotEqual(t, code1, code2)

	// 旧码绑定应失败(已作废)
	err = svc.BindFeishu(code1, "ou_openid_new")
	assert.ErrorIs(t, err, ErrCodeInvalid)
}

// ---------- BindFeishu ----------

func TestBindingService_BindFeishu_Success(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	code, _, err := svc.GenerateCode(1)
	require.NoError(t, err)

	err = svc.BindFeishu(code, "ou_openid_alice")
	require.NoError(t, err)

	// 绑定后能解析出 user_id
	uid, bound, err := svc.ResolveFeishuUserID("ou_openid_alice")
	require.NoError(t, err)
	assert.True(t, bound)
	assert.Equal(t, int64(1), uid)

	// 绑定后码应已删(幂等:再次用同码失败)
	err = svc.BindFeishu(code, "ou_openid_alice")
	assert.ErrorIs(t, err, ErrCodeInvalid)
}

func TestBindingService_BindFeishu_CodeInvalid(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	err := svc.BindFeishu("999999", "ou_openid_x")
	assert.ErrorIs(t, err, ErrCodeInvalid)
}

func TestBindingService_BindFeishu_CodeExpired(t *testing.T) {
	svc, _, _ := newBindingService(t, -1*time.Second) // TTL 为负,生成即过期

	code, _, err := svc.GenerateCode(1)
	require.NoError(t, err)

	err = svc.BindFeishu(code, "ou_openid_x")
	assert.ErrorIs(t, err, ErrCodeInvalid)
}

func TestBindingService_BindFeishu_FeishuAlreadyBound(t *testing.T) {
	svc, db, _ := newBindingService(t, 5*time.Minute)

	// 预置:ou_openid_alice 已绑到账号 1
	ch := domain.NewUserChannel(1, "feishu", "ou_openid_alice")
	require.NoError(t, db.Create(ch).Error)

	// 账号 2 生成码,试图绑同一个飞书号 -> 应拒
	code, _, err := svc.GenerateCode(2)
	require.NoError(t, err)

	err = svc.BindFeishu(code, "ou_openid_alice")
	assert.ErrorIs(t, err, ErrFeishuAlreadyBound)
}

func TestBindingService_BindFeishu_AccountAlreadyBound(t *testing.T) {
	// 账号 3 已绑 ou_openid_prev;用 repo 直接插一个码(绕过 GenerateCode 的已绑检查),
	// 再 BindFeishu 该账号的新 open_id -> 应因账号已绑返回 ErrAccountAlreadyBound
	svc, db, bindCodeRepo := newBindingService(t, 5*time.Minute)
	ch := domain.NewUserChannel(3, "feishu", "ou_openid_prev")
	require.NoError(t, db.Create(ch).Error)

	require.NoError(t, bindCodeRepo.Upsert(&domain.FeishuBindCode{
		UserID:    3,
		Code:      "314159",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}))

	err := svc.BindFeishu("314159", "ou_openid_new")
	assert.ErrorIs(t, err, ErrAccountAlreadyBound)
}

// ---------- ResolveFeishuUserID ----------

func TestBindingService_ResolveFeishuUserID_NotBound(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	uid, bound, err := svc.ResolveFeishuUserID("ou_openid_nobody")
	require.NoError(t, err)
	assert.False(t, bound)
	assert.Equal(t, int64(0), uid)
}

func TestBindingService_GenerateCode_Randomness(t *testing.T) {
	// 确认 6 位码格式 + 不重复生成相同码(概率)
	svc, _, _ := newBindingService(t, 5*time.Minute)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		// 每次新账号生成
		code, _, err := svc.GenerateCode(int64(i + 1))
		require.NoError(t, err)
		assert.Len(t, code, 6)
		for _, r := range code {
			assert.GreaterOrEqual(t, r, rune('0'))
			assert.LessOrEqual(t, r, rune('9'))
		}
		seen[code] = true
	}
	// 100 个码里应有足够多样性(放宽:>= 80 个不同)
	assert.Greater(t, len(seen), 80)
}

// ---------- helpers ----------

// GormUserChannelRepositoryShim 复用真实 GormUserChannelRepository,但用 service 包接口
// (service 包定义了自己的 UserChannelRepository 接口,repo 包实现与之兼容)
func NewGormUserChannelRepositoryShim(db *gorm.DB) UserChannelRepository {
	return &gormUserChannelShim{db: db}
}

type gormUserChannelShim struct{ db *gorm.DB }

func (s *gormUserChannelShim) Create(uc *domain.UserChannel) error { return s.db.Create(uc).Error }
func (s *gormUserChannelShim) GetByChannel(channelType, channelUserID string) (*domain.UserChannel, error) {
	var uc domain.UserChannel
	err := s.db.Where("channel_type = ? AND channel_user_id = ?", channelType, channelUserID).First(&uc).Error
	if err != nil {
		return nil, err
	}
	return &uc, nil
}
func (s *gormUserChannelShim) GetByUserID(userID int64) ([]*domain.UserChannel, error) {
	var ucs []*domain.UserChannel
	err := s.db.Where("user_id = ?", userID).Find(&ucs).Error
	if err != nil {
		return nil, err
	}
	return ucs, nil
}
