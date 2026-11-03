package search

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/search"
	"omnibot/internal/pkg/crypto"
	repo "omnibot/internal/repository/search"
)

func setupServiceEnv(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	crypto.Init("search-service-test-key")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.Config{}))
	return NewService(repo.NewConfigRepository(db)), db
}

func setupService(t *testing.T) *Service {
	t.Helper()
	svc, _ := setupServiceEnv(t)
	return svc
}

func boolPtr(v bool) *bool { return &v }

// N3 配置 CRUD:新建默认启用、密钥加密、视图脱敏。
func TestService_SaveCreatesWithDefaults(t *testing.T) {
	svc := setupService(t)

	view, err := svc.SaveConfig(1, SaveInput{APIKey: "sk-test-key-1234567890"})
	require.NoError(t, err)

	assert.True(t, view.HasConfig)
	assert.True(t, view.Enabled, "新建默认启用")
	assert.Equal(t, domain.ProviderDeepSeek, view.Provider)
	assert.Equal(t, domain.DefaultBaseURL, view.BaseURL)
	assert.Equal(t, domain.DefaultCount, view.DefaultCount)
	assert.Equal(t, domain.DefaultTimeoutSeconds, view.TimeoutSeconds)
	assert.Equal(t, "sk-te...90", view.APIKeyMasked)
	assert.NotContains(t, view.APIKeyMasked, "1234567890", "掩码不能泄露中段")
}

func TestService_SaveRejectsInvalidCount(t *testing.T) {
	svc := setupService(t)

	_, err := svc.SaveConfig(1, SaveInput{APIKey: "sk-x-123456", DefaultCount: 99})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "条数")

	view, err := svc.GetView(1)
	require.NoError(t, err)
	assert.False(t, view.HasConfig, "校验失败不应落库")
}

// 密钥留空 = 保持原值(前端不回显明文,用户只改地址时不必重填 Key)。
func TestService_SaveEmptyKeyKeepsPrevious(t *testing.T) {
	svc := setupService(t)

	_, err := svc.SaveConfig(1, SaveInput{APIKey: "sk-original-key-abcdef"})
	require.NoError(t, err)

	_, err = svc.SaveConfig(1, SaveInput{BaseURL: "https://proxy.example.com", DefaultCount: 8})
	require.NoError(t, err)

	cfg, err := svc.GetConfig(1)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "https://proxy.example.com", cfg.BaseURL)
	assert.Equal(t, 8, cfg.DefaultCount)

	plain, err := crypto.Decrypt(cfg.APIKey)
	require.NoError(t, err)
	assert.Equal(t, "sk-original-key-abcdef", plain, "留空时必须保留原密钥")
}

func TestService_SaveClearKeyDisablesUsable(t *testing.T) {
	svc := setupService(t)

	_, err := svc.SaveConfig(1, SaveInput{APIKey: "sk-to-be-cleared-1234"})
	require.NoError(t, err)

	view, err := svc.SaveConfig(1, SaveInput{ClearKey: true})
	require.NoError(t, err)
	assert.Equal(t, "", view.APIKeyMasked)
	assert.False(t, view.HasConfig)

	cfg, err := svc.GetConfig(1)
	require.NoError(t, err)
	assert.False(t, cfg.IsUsable())
}

func TestService_SaveDisabledPersists(t *testing.T) {
	svc := setupService(t)

	_, err := svc.SaveConfig(1, SaveInput{APIKey: "sk-key-1234567890", Enabled: boolPtr(false)})
	require.NoError(t, err)

	cfg, err := svc.GetConfig(1)
	require.NoError(t, err)
	assert.False(t, cfg.Enabled, "关闭开关必须能落库")
	assert.False(t, cfg.IsUsable())
}

func TestService_GetViewNoConfig(t *testing.T) {
	svc := setupService(t)

	view, err := svc.GetView(1)
	require.NoError(t, err)
	assert.False(t, view.HasConfig)
	assert.Equal(t, domain.DefaultBaseURL, view.BaseURL, "未配置时给前端默认地址方便直接填")
}

func TestService_ClearConfig(t *testing.T) {
	svc := setupService(t)

	_, err := svc.SaveConfig(1, SaveInput{APIKey: "sk-key-1234567890"})
	require.NoError(t, err)

	require.NoError(t, svc.ClearConfig(1))

	view, err := svc.GetView(1)
	require.NoError(t, err)
	assert.False(t, view.HasConfig)
}

// 多用户隔离:配置按 user_id 分开,互不可见。
func TestService_UserIsolation(t *testing.T) {
	svc := setupService(t)

	_, err := svc.SaveConfig(1, SaveInput{APIKey: "sk-user1-key-12345"})
	require.NoError(t, err)

	view2, err := svc.GetView(2)
	require.NoError(t, err)
	assert.False(t, view2.HasConfig)
}

// 密文损坏(换过密钥/数据损坏)→ 视为未配置,不 panic、不泄露密文。
func TestService_CorruptedCipherTreatedAsNotConfigured(t *testing.T) {
	svc, db := setupServiceEnv(t)

	raw := &domain.Config{
		UserID: 1, Provider: domain.ProviderDeepSeek, BaseURL: domain.DefaultBaseURL,
		APIKey: "not-a-valid-ciphertext", Enabled: true, DefaultCount: 5, TimeoutSeconds: 20,
	}
	require.NoError(t, db.Create(raw).Error)

	cfg, err := svc.GetConfig(1)
	require.NoError(t, err)
	assert.Nil(t, cfg, "解密失败按未配置处理,且不返回密文")

	_, err = svc.Search(context.Background(), 1, "任意查询", 0)
	require.ErrorIs(t, err, ErrNotConfigured)
}

// need_content 是"可关"的业务值:未传 = 保持,显式 false = 关闭。
func TestService_NeedContentPreserveSemantics(t *testing.T) {
	svc := setupService(t)

	_, err := svc.SaveConfig(1, SaveInput{APIKey: "sk-key-1234567890", NeedContent: boolPtr(true)})
	require.NoError(t, err)

	// 只改地址,不传 need_content → 保持 true
	_, err = svc.SaveConfig(1, SaveInput{BaseURL: "https://proxy.example.com"})
	require.NoError(t, err)
	cfg, err := svc.GetConfig(1)
	require.NoError(t, err)
	assert.True(t, cfg.NeedContent)

	// 显式传 false → 关闭
	_, err = svc.SaveConfig(1, SaveInput{NeedContent: boolPtr(false)})
	require.NoError(t, err)
	cfg, err = svc.GetConfig(1)
	require.NoError(t, err)
	assert.False(t, cfg.NeedContent)
}
