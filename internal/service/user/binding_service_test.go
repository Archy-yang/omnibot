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
	err = db.AutoMigrate(&domain.User{}, &domain.UserChannel{}, &domain.BindCode{})
	require.NoError(t, err)
	return db
}

func newBindingService(t *testing.T, ttl time.Duration) (*BindingService, *gorm.DB, userrepo.BindCodeRepository) {
	db := setupBindingTestDB(t)
	channelRepo := NewGormUserChannelRepositoryShim(db)
	bindCodeRepo := userrepo.NewBindCodeRepository(db)
	svc := NewBindingService(channelRepo, bindCodeRepo, ttl)
	return svc, db, bindCodeRepo
}

// ---------- IsFeishuBound ----------

func TestBindingService_IsFeishuBound_NotBound(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)
	bound, err := svc.IsChannelBound(1, "feishu")
	require.NoError(t, err)
	assert.False(t, bound)
}

func TestBindingService_IsFeishuBound_Bound(t *testing.T) {
	svc, db, _ := newBindingService(t, 5*time.Minute)
	// 直接写入一条飞书绑定
	ch := domain.NewUserChannel(1, "feishu", "ou_openid_xxx")
	require.NoError(t, db.Create(ch).Error)

	bound, err := svc.IsChannelBound(1, "feishu")
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
	bound, err := svc.IsChannelBound(1, "feishu")
	require.NoError(t, err)
	assert.False(t, bound)
}

func TestBindingService_GenerateCode_AllowsEvenIfFeishuBound(t *testing.T) {
	// v2.3: 一账号可同时绑飞书+微信。账号已绑飞书时仍可出码(用于绑微信)。
	// "两渠道都已绑"的拦截在 web handler 层,不在 GenerateCode。
	svc, db, _ := newBindingService(t, 5*time.Minute)
	ch := domain.NewUserChannel(1, "feishu", "ou_openid_xxx")
	require.NoError(t, db.Create(ch).Error)

	_, _, err := svc.GenerateCode(1)
	assert.NoError(t, err)
}

func TestBindingService_GenerateCode_RegenerateInvalidatesOld(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	code1, _, err := svc.GenerateCode(1)
	require.NoError(t, err)
	code2, _, err := svc.GenerateCode(1)
	require.NoError(t, err)
	assert.NotEqual(t, code1, code2)

	// 旧码绑定应失败(已作废)
	err = svc.BindChannel("feishu", code1, "ou_openid_new")
	assert.ErrorIs(t, err, ErrCodeInvalid)
}

// ---------- BindFeishu ----------

func TestBindingService_BindFeishu_Success(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	code, _, err := svc.GenerateCode(1)
	require.NoError(t, err)

	err = svc.BindChannel("feishu", code, "ou_openid_alice")
	require.NoError(t, err)

	// 绑定后能解析出 user_id
	uid, bound, err := svc.ResolveUserID("feishu", "ou_openid_alice")
	require.NoError(t, err)
	assert.True(t, bound)
	assert.Equal(t, int64(1), uid)

	// 绑定后码应已删(幂等:再次用同码失败)
	err = svc.BindChannel("feishu", code, "ou_openid_alice")
	assert.ErrorIs(t, err, ErrCodeInvalid)
}

func TestBindingService_BindFeishu_CodeInvalid(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	err := svc.BindChannel("feishu", "999999", "ou_openid_x")
	assert.ErrorIs(t, err, ErrCodeInvalid)
}

func TestBindingService_BindFeishu_CodeExpired(t *testing.T) {
	svc, _, _ := newBindingService(t, -1*time.Second) // TTL 为负,生成即过期

	code, _, err := svc.GenerateCode(1)
	require.NoError(t, err)

	err = svc.BindChannel("feishu", code, "ou_openid_x")
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

	err = svc.BindChannel("feishu", code, "ou_openid_alice")
	assert.ErrorIs(t, err, ErrChannelAlreadyBound)
}

func TestBindingService_BindFeishu_AccountAlreadyBound(t *testing.T) {
	// 账号 3 已绑 ou_openid_prev;用 repo 直接插一个码(绕过 GenerateCode 的已绑检查),
	// 再 BindFeishu 该账号的新 open_id -> 应因账号已绑返回 ErrAccountAlreadyBound
	svc, db, bindCodeRepo := newBindingService(t, 5*time.Minute)
	ch := domain.NewUserChannel(3, "feishu", "ou_openid_prev")
	require.NoError(t, db.Create(ch).Error)

	require.NoError(t, bindCodeRepo.Upsert(&domain.BindCode{
		UserID:    3,
		Code:      "314159",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}))

	err := svc.BindChannel("feishu", "314159", "ou_openid_new")
	assert.ErrorIs(t, err, ErrAccountAlreadyBound)
}

// ---------- ResolveFeishuUserID ----------

func TestBindingService_ResolveFeishuUserID_NotBound(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	uid, bound, err := svc.ResolveUserID("feishu", "ou_openid_nobody")
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

// ===== v2.3 渠道通用 + 微信绑定 测试 =====

// 通用码:一个码在飞书发绑飞书,在微信发绑微信(各需独立出码,码只用一次)。
func TestBindingService_BindChannel_GenericCode_WorksForBothChannels(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)

	// 出码1 -> 绑飞书
	code1, _, err := svc.GenerateCode(1)
	require.NoError(t, err)
	require.NoError(t, svc.BindChannel("feishu", code1, "ou_feishu_alice"))
	uid, bound, err := svc.ResolveUserID("feishu", "ou_feishu_alice")
	require.NoError(t, err)
	assert.True(t, bound)
	assert.Equal(t, int64(1), uid)

	// 出码2 -> 绑微信(同一账号,不同渠道)
	code2, _, err := svc.GenerateCode(1)
	require.NoError(t, err)
	require.NoError(t, svc.BindChannel("wechat", code2, "wx_wechat_bob"))
	uid2, bound2, err := svc.ResolveUserID("wechat", "wx_wechat_bob")
	require.NoError(t, err)
	assert.True(t, bound2)
	assert.Equal(t, int64(1), uid2)

	// 两渠道都绑到账号1
	feishuBound, _ := svc.IsChannelBound(1, "feishu")
	wechatBound, _ := svc.IsChannelBound(1, "wechat")
	assert.True(t, feishuBound)
	assert.True(t, wechatBound)
}

// 码只用一次:绑飞书后再用同码绑微信应失败(码已删)。
func TestBindingService_BindChannel_CodeSingleUse(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)
	code, _, err := svc.GenerateCode(1)
	require.NoError(t, err)

	require.NoError(t, svc.BindChannel("feishu", code, "ou_x"))
	err = svc.BindChannel("wechat", code, "wx_y")
	assert.ErrorIs(t, err, ErrCodeInvalid)
}

// 微信号已绑其他账号 -> ErrChannelAlreadyBound。
func TestBindingService_BindChannel_WeChatAlreadyBound(t *testing.T) {
	svc, db, _ := newBindingService(t, 5*time.Minute)
	// wx_bob 已绑账号 1
	require.NoError(t, db.Create(domain.NewUserChannel(1, "wechat", "wx_bob")).Error)

	// 账号 2 出码绑同一微信号 -> 拒
	code, _, err := svc.GenerateCode(2)
	require.NoError(t, err)
	err = svc.BindChannel("wechat", code, "wx_bob")
	assert.ErrorIs(t, err, ErrChannelAlreadyBound)
}

// 账号已绑微信,再用新码绑另一个微信号 -> ErrAccountAlreadyBound。
func TestBindingService_BindChannel_AccountAlreadyBoundWeChat(t *testing.T) {
	svc, db, bindCodeRepo := newBindingService(t, 5*time.Minute)
	require.NoError(t, db.Create(domain.NewUserChannel(3, "wechat", "wx_old")).Error)
	require.NoError(t, bindCodeRepo.Upsert(&domain.BindCode{
		UserID: 3, Code: "271828", ExpiresAt: time.Now().Add(5 * time.Minute),
	}))

	err := svc.BindChannel("wechat", "271828", "wx_new")
	assert.ErrorIs(t, err, ErrAccountAlreadyBound)
}

// 绑飞书不影响绑微信:跨渠道独立。
func TestBindingService_BindChannel_CrossChannelIndependent(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)
	// 账号 1 已绑飞书
	code1, _, _ := svc.GenerateCode(1)
	require.NoError(t, svc.BindChannel("feishu", code1, "ou_f"))

	// 同一微信号绑到账号 2(不受账号1绑飞书影响)
	code2, _, _ := svc.GenerateCode(2)
	require.NoError(t, svc.BindChannel("wechat", code2, "wx_w"))
	uid, bound, _ := svc.ResolveUserID("wechat", "wx_w")
	assert.True(t, bound)
	assert.Equal(t, int64(2), uid)
}

// ResolveUserID 微信未绑返回 (0,false)。
func TestBindingService_ResolveUserID_WeChatNotBound(t *testing.T) {
	svc, _, _ := newBindingService(t, 5*time.Minute)
	uid, bound, err := svc.ResolveUserID("wechat", "wx_nobody")
	require.NoError(t, err)
	assert.False(t, bound)
	assert.Equal(t, int64(0), uid)
}
