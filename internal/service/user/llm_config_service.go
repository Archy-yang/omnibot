package user

import (
	"errors"
	"gorm.io/gorm"
	"strings"
	"time"
	"wechat-intelligent-bot/internal/domain/user"
	"wechat-intelligent-bot/internal/pkg/crypto"
	repo "wechat-intelligent-bot/internal/repository/user"
)

// LLMConfigService LLM 配置服务接口
type LLMConfigService interface {
	GetConfigForUser(userID int64) (apiKey, baseURL, model string, hasCustomConfig bool, err error)
	SetAPIKey(userID int64, apiKey string) error
	SetBaseURL(userID int64, baseURL string) error
	SetModel(userID int64, model string) error
	GetConfigView(userID int64) (*LLMConfigView, error)
	ClearConfig(userID int64) error
}

// LLMConfigView 配置视图，用于前端展示
type LLMConfigView struct {
	HasConfig    bool
	APIKeyMasked string
	BaseURL      string
	Model        string
	StatusText   string
}

// GormLLMConfigService GORM 实现
type GormLLMConfigService struct {
	repo repo.LLMConfigRepository
}

// NewLLMConfigService 创建服务
func NewLLMConfigService(repo repo.LLMConfigRepository) LLMConfigService {
	return &GormLLMConfigService{repo: repo}
}

func (s *GormLLMConfigService) GetConfigForUser(userID int64) (string, string, string, bool, error) {
	cfg, err := s.repo.GetByUserID(userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", "", "", false, nil
		}
		return "", "", "", false, err
	}

	if !cfg.IsEnabled() {
		return "", "", "", false, nil
	}

	// 解密 API Key
	plainKey, err := crypto.Decrypt(cfg.APIKey)
	if err != nil {
		return "", "", "", false, err
	}

	return plainKey, cfg.GetBaseURL(), cfg.GetModel(), true, nil
}

func (s *GormLLMConfigService) SetAPIKey(userID int64, apiKey string) error {
	// 格式验证
	if !strings.HasPrefix(apiKey, "sk-") {
		return errors.New("API Key 必须以 sk- 开头")
	}
	if len(apiKey) < 30 || len(apiKey) > 200 {
		return errors.New("API Key 长度不正确")
	}

	// 加密
	encrypted, err := crypto.Encrypt(apiKey)
	if err != nil {
		return err
	}

	// 检查是否已存在配置
	cfg, err := s.repo.GetByUserID(userID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}

	now := time.Now()

	if err == gorm.ErrRecordNotFound {
		// 创建新配置
		cfg = &user.LLMConfig{
			UserID:    userID,
			APIKey:    encrypted,
			Status:    user.LLMConfigStatusNormal,
			CreatedAt: now,
			UpdatedAt: now,
		}
		return s.repo.Create(cfg)
	}

	// 更新现有配置
	cfg.APIKey = encrypted
	cfg.UpdatedAt = now
	return s.repo.Update(cfg)
}

func (s *GormLLMConfigService) SetBaseURL(userID int64, baseURL string) error {
	cfg, err := s.repo.GetByUserID(userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return errors.New("请先设置 API Key")
		}
		return err
	}

	// URL 格式简单验证
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return errors.New("API 地址必须以 http:// 或 https:// 开头")
	}

	cfg.BaseURL = &baseURL
	cfg.UpdatedAt = time.Now()
	return s.repo.Update(cfg)
}

func (s *GormLLMConfigService) SetModel(userID int64, model string) error {
	cfg, err := s.repo.GetByUserID(userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return errors.New("请先设置 API Key")
		}
		return err
	}

	if len(model) > 128 {
		return errors.New("模型名太长")
	}

	cfg.Model = &model
	cfg.UpdatedAt = time.Now()
	return s.repo.Update(cfg)
}

func (s *GormLLMConfigService) GetConfigView(userID int64) (*LLMConfigView, error) {
	cfg, err := s.repo.GetByUserID(userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return &LLMConfigView{
				HasConfig:  false,
				StatusText: "使用系统默认模型",
			}, nil
		}
		return nil, err
	}

	if !cfg.IsEnabled() {
		return &LLMConfigView{
			HasConfig:  false,
			StatusText: "使用系统默认模型",
		}, nil
	}

	// 脱敏 API Key
	maskedKey := s.maskAPIKey(cfg.APIKey)

	return &LLMConfigView{
		HasConfig:    true,
		APIKeyMasked: maskedKey,
		BaseURL:      cfg.GetBaseURL(),
		Model:        cfg.GetModel(),
		StatusText:   "使用你的自定义模型",
	}, nil
}

func (s *GormLLMConfigService) maskAPIKey(encryptedKey string) string {
	// 先解密，再脱敏
	plain, err := crypto.Decrypt(encryptedKey)
	if err != nil {
		return "sk-***"
	}
	if len(plain) <= 6 {
		return "sk-***"
	}
	return plain[:5] + "..." + plain[len(plain)-2:]
}

func (s *GormLLMConfigService) ClearConfig(userID int64) error {
	return s.repo.Delete(userID)
}
