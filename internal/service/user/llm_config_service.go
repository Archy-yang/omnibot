package user

import (
	"errors"
	"gorm.io/gorm"
	"omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"
	repo "omnibot/internal/repository/user"
	"strings"
	"time"
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
	ListProviderOptions() []ProviderOption
	// GetEmbeddingConfigForUser 用户级向量配置(12-记忆系统技术方案 §5.3):
	// 返回解密后的完整配置;未配置/不完整返回 false(装配点回落系统默认)。
	GetEmbeddingConfigForUser(userID int64) (*EmbeddingAPIConfig, bool, error)
}

// EmbeddingAPIConfig 用户级向量配置(解密后,供 memory 层构造 provider)。
type EmbeddingAPIConfig struct {
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
	Dims     int
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
	// 用户级向量配置(可选):全空=不设置;部分填写=校验拒绝
	EmbeddingProvider string
	EmbeddingBaseURL  string
	EmbeddingAPIKey   string
	EmbeddingModel    string
	EmbeddingDims     int
}

// GormLLMConfigService GORM 实现
type GormLLMConfigService struct {
	repo repo.LLMConfigRepository
}

// NewLLMConfigService 创建服务
func NewLLMConfigService(repo repo.LLMConfigRepository) LLMConfigService {
	return &GormLLMConfigService{repo: repo}
}

func (s *GormLLMConfigService) ListProviderOptions() []ProviderOption {
	return listProviderOptions()
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

// embeddingFieldsState 嵌入配置字段状态:(是否有任一字段填写, 五要素是否齐全)。
func embeddingFieldsState(req UpdateConfigRequest) (hasAny bool, complete bool) {
	hasAny = req.EmbeddingProvider != "" || req.EmbeddingBaseURL != "" ||
		req.EmbeddingAPIKey != "" || req.EmbeddingModel != "" || req.EmbeddingDims != 0
	complete = req.EmbeddingProvider != "" && req.EmbeddingBaseURL != "" &&
		req.EmbeddingAPIKey != "" && req.EmbeddingModel != "" && req.EmbeddingDims > 0
	return
}

// GetEmbeddingConfigForUser 返回解密后的用户级向量配置(12-记忆系统技术方案 §5.3)。
// 未配置/不完整/解密失败 → false;密文解密失败按无配置降级(装配点回落系统默认)。
func (s *GormLLMConfigService) GetEmbeddingConfigForUser(userID int64) (*EmbeddingAPIConfig, bool, error) {
	cfg, err := s.repo.GetByUserID(userID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !cfg.HasEmbeddingConfig() {
		return nil, false, nil
	}
	plainKey, err := crypto.Decrypt(cfg.EmbeddingAPIKey)
	if err != nil {
		return nil, false, nil
	}
	return &EmbeddingAPIConfig{
		Provider: *cfg.EmbeddingProvider,
		BaseURL:  *cfg.EmbeddingBaseURL,
		APIKey:   plainKey,
		Model:    *cfg.EmbeddingModel,
		Dims:     *cfg.EmbeddingDims,
	}, true, nil
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
	providerOption, knownProvider := findProviderOption(req.Provider)
	if !knownProvider && !isLegacyProvider(req.Provider) {
		return errors.New("不支持的服务商")
	}
	if knownProvider && providerOption.Status == ProviderStatusDisabled {
		return errors.New(nativeProviderDisabledReason)
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

	// 用户级向量配置校验(12-记忆系统技术方案 §5.3):全空=不设置,部分填写=拒绝
	if hasAny, complete := embeddingFieldsState(req); hasAny && !complete {
		return errors.New("向量配置不完整：provider、API 地址、API Key、模型、维度需全部填写")
	}
	if req.EmbeddingProvider != "" {
		if !user.EmbeddingProviderAllowed(req.EmbeddingProvider) {
			return errors.New("不支持的向量服务商，支持: openai_compatible, ollama")
		}
		if !strings.HasPrefix(req.EmbeddingBaseURL, "http://") && !strings.HasPrefix(req.EmbeddingBaseURL, "https://") {
			return errors.New("向量 API 地址必须以 http:// 或 https:// 开头")
		}
		if len(req.EmbeddingAPIKey) < 10 || len(req.EmbeddingAPIKey) > 512 {
			return errors.New("向量 API Key 长度不正确")
		}
	}

	// 自定义 OpenAI-compatible 必须提供 BaseURL 和 Model
	if req.Provider == "custom_openai_compatible" {
		if req.BaseURL == "" {
			return errors.New("请输入 API 地址")
		}
		if req.Model == "" {
			return errors.New("请输入模型名称")
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
			UserID:    userID,
			Provider:  req.Provider,
			APIKey:    encryptedKey,
			Status:    user.LLMConfigStatusNormal,
			CreatedAt: now,
			UpdatedAt: now,
		}
		baseURL := req.BaseURL
		if baseURL == "" && knownProvider {
			baseURL = providerOption.DefaultBaseURL
		}
		if baseURL != "" {
			cfg.BaseURL = &baseURL
		}
		model := req.Model
		if model == "" && knownProvider {
			model = providerOption.DefaultModel
		}
		if model != "" {
			cfg.Model = &model
		}
		temp := req.Temperature
		cfg.Temperature = &temp
		tokens := req.MaxTokens
		cfg.MaxTokens = &tokens

		if hasAny, _ := embeddingFieldsState(req); hasAny {
			encryptedEmbedKey, err := crypto.Encrypt(req.EmbeddingAPIKey)
			if err != nil {
				return err
			}
			provider := req.EmbeddingProvider
			cfg.EmbeddingProvider = &provider
			base := req.EmbeddingBaseURL
			cfg.EmbeddingBaseURL = &base
			cfg.EmbeddingAPIKey = encryptedEmbedKey
			embedModel := req.EmbeddingModel
			cfg.EmbeddingModel = &embedModel
			dims := req.EmbeddingDims
			cfg.EmbeddingDims = &dims
		}

		return s.repo.Create(cfg)
	}

	// 更新现有配置
	cfg.Provider = req.Provider
	if req.APIKey != "" {
		cfg.APIKey = encryptedKey
	}
	baseURL := req.BaseURL
	if baseURL == "" && knownProvider {
		baseURL = providerOption.DefaultBaseURL
	}
	if baseURL != "" {
		cfg.BaseURL = &baseURL
	}
	model := req.Model
	if model == "" && knownProvider {
		model = providerOption.DefaultModel
	}
	if model != "" {
		cfg.Model = &model
	}
	temp := req.Temperature
	cfg.Temperature = &temp
	tokens := req.MaxTokens
	cfg.MaxTokens = &tokens

	// 用户级向量配置(全空=不改动既有嵌入配置;部分填写已在前面校验拒绝)
	if hasAny, _ := embeddingFieldsState(req); hasAny {
		encryptedEmbedKey, err := crypto.Encrypt(req.EmbeddingAPIKey)
		if err != nil {
			return err
		}
		provider := req.EmbeddingProvider
		cfg.EmbeddingProvider = &provider
		base := req.EmbeddingBaseURL
		cfg.EmbeddingBaseURL = &base
		cfg.EmbeddingAPIKey = encryptedEmbedKey
		embedModel := req.EmbeddingModel
		cfg.EmbeddingModel = &embedModel
		dims := req.EmbeddingDims
		cfg.EmbeddingDims = &dims
	}
	cfg.UpdatedAt = now

	return s.repo.Update(cfg)
}
