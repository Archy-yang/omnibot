package user

import (
	"errors"
	"gorm.io/gorm"
	"strings"
	"time"
	"omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"
	repo "omnibot/internal/repository/user"
)

// LLMConfigService LLM 配置服务接口
type LLMConfigService interface {
	GetConfigForUser(userID int64) (apiKey, baseURL, model string, hasCustomConfig bool, err error)
	GetFullConfigForUser(userID int64) (*FullLLMConfig, bool, error)
	SetAPIKey(userID int64, apiKey string) error
	SetBaseURL(userID int64, baseURL string) error
	SetModel(userID int64, model string) error
	UpdateFullConfig(userID int64, config UpdateConfigRequest) error
	GetConfigView(userID int64) (*LLMConfigView, error)
	ClearConfig(userID int64) error
}

// LLMConfigView 配置视图，用于前端展示
type LLMConfigView struct {
	HasConfig    bool
	APIKeyMasked string
	BaseURL      string
	Model        string
	Provider     string
	StatusText   string
	Temperature  float64
	MaxTokens    int
}

// FullLLMConfig 完整配置，用于 LLM 客户端创建
type FullLLMConfig struct {
	APIKey      string
	BaseURL     string
	Model       string
	Provider    string
	Temperature float64
	MaxTokens   int
}

// UpdateConfigRequest 更新配置请求
type UpdateConfigRequest struct {
	Provider    string
	APIKey      string
	BaseURL     string
	Model       string
	Temperature float64
	MaxTokens   int
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
		Provider:     cfg.Provider,
		StatusText:   "使用你的自定义模型",
		Temperature:  cfg.GetTemperature(0.7),
		MaxTokens:    cfg.GetMaxTokens(2048),
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

// GetFullConfigForUser 获取用户完整配置，用于创建 LLM 客户端
func (s *GormLLMConfigService) GetFullConfigForUser(userID int64) (*FullLLMConfig, bool, error) {
	cfg, err := s.repo.GetByUserID(userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}

	if !cfg.IsEnabled() {
		return nil, false, nil
	}

	// 解密 API Key
	plainKey, err := crypto.Decrypt(cfg.APIKey)
	if err != nil {
		return nil, false, err
	}

	return &FullLLMConfig{
		APIKey:      plainKey,
		BaseURL:     cfg.GetBaseURL(),
		Model:       cfg.GetModel(),
		Provider:    cfg.Provider,
		Temperature: cfg.GetTemperature(0.7),
		MaxTokens:   cfg.GetMaxTokens(2048),
	}, true, nil
}

// UpdateFullConfig 完整更新用户配置
func (s *GormLLMConfigService) UpdateFullConfig(userID int64, req UpdateConfigRequest) error {
	// API Key 格式验证
	if req.APIKey != "" {
		if len(req.APIKey) < 10 || len(req.APIKey) > 512 {
			return errors.New("API Key 长度不正确")
		}
	}

	// 验证服务商
	validProviders := map[string]bool{
		"openai":    true,
		"anthropic": true,
		"azure":     true,
		"qwen":      true,
		"doubao":    true,
	}
	if !validProviders[req.Provider] {
		return errors.New("不支持的服务商")
	}

	// 验证温度范围
	if req.Temperature < 0 || req.Temperature > 2 {
		return errors.New("温度参数必须在 0-2 之间")
	}

	// 验证 MaxTokens
	if req.MaxTokens < 0 || req.MaxTokens > 128000 {
		return errors.New("MaxTokens 超出范围")
	}

	// URL 格式验证
	if req.BaseURL != "" {
		if !strings.HasPrefix(req.BaseURL, "http://") && !strings.HasPrefix(req.BaseURL, "https://") {
			return errors.New("API 地址必须以 http:// 或 https:// 开头")
		}
	}

	// 检查是否已存在配置
	cfg, err := s.repo.GetByUserID(userID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}

	now := time.Now()

	// 加密 API Key（如果提供了）
	var encryptedKey string
	if req.APIKey != "" {
		encrypted, err := crypto.Encrypt(req.APIKey)
		if err != nil {
			return err
		}
		encryptedKey = encrypted
	}

	if err == gorm.ErrRecordNotFound {
		// 创建新配置 - 必须提供 API Key
		if req.APIKey == "" {
			return errors.New("首次配置必须提供 API Key")
		}

		cfg = &user.LLMConfig{
			UserID:      userID,
			Provider:    req.Provider,
			APIKey:      encryptedKey,
			Status:      user.LLMConfigStatusNormal,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if req.BaseURL != "" {
			cfg.BaseURL = &req.BaseURL
		}
		if req.Model != "" {
			cfg.Model = &req.Model
		}
		temp := req.Temperature
		cfg.Temperature = &temp
		tokens := req.MaxTokens
		cfg.MaxTokens = &tokens

		return s.repo.Create(cfg)
	}

	// 更新现有配置
	cfg.Provider = req.Provider
	if req.APIKey != "" {
		cfg.APIKey = encryptedKey
	}
	if req.BaseURL != "" {
		cfg.BaseURL = &req.BaseURL
	}
	if req.Model != "" {
		cfg.Model = &req.Model
	}
	temp := req.Temperature
	cfg.Temperature = &temp
	tokens := req.MaxTokens
	cfg.MaxTokens = &tokens
	cfg.UpdatedAt = now

	return s.repo.Update(cfg)
}
