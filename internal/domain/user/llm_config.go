package user

import (
	"time"
)

// LLMConfig 用户自定义 LLM 配置
type LLMConfig struct {
	ID          int64    `gorm:"primaryKey;autoIncrement"`
	UserID      int64    `gorm:"not null;uniqueIndex"`
	Provider    string   `gorm:"size:64;not null;default:'openai'"` // 服务商：openai/anthropic/azure/qwen/doubao/baidu_qianfan/volcengine/aliyun_qwen/custom_openai_compatible
	APIKey      string   `gorm:"size:512;not null"`                 // 加密后存储
	BaseURL     *string  `gorm:"size:256"`
	Model       *string  `gorm:"size:128"`
	Temperature *float64 `gorm:"type:decimal(3,2)"` // 0-2，保留两位小数
	MaxTokens   *int     `gorm:"type:int"`
	// 用户级向量配置(12-记忆系统技术方案 §5.3):全空=用系统默认;APIKey 加密存储。
	EmbeddingProvider *string `gorm:"size:32"` // openai_compatible | ollama
	EmbeddingBaseURL  *string `gorm:"size:256"`
	EmbeddingAPIKey   string  `gorm:"size:512"` // 加密后存储
	EmbeddingModel    *string `gorm:"size:128"`
	EmbeddingDims     *int
	Status            int8      `gorm:"default:0;not null"` // 0-正常, 1-禁用
	CreatedAt         time.Time `gorm:"not null"`
	UpdatedAt         time.Time `gorm:"not null"`
}

// 用户级向量配置支持的 provider(与 memory.EmbeddingProviderConfig 对齐)
var embeddingProviders = map[string]bool{
	"openai_compatible": true,
	"ollama":            true,
}

// EmbeddingProviderAllowed provider 是否在支持列表内。
func EmbeddingProviderAllowed(provider string) bool {
	return embeddingProviders[provider]
}

// HasEmbeddingConfig 用户级向量配置是否完整(五要素齐全才视为有效)。
func (c *LLMConfig) HasEmbeddingConfig() bool {
	return c.EmbeddingProvider != nil && *c.EmbeddingProvider != "" &&
		c.EmbeddingBaseURL != nil && *c.EmbeddingBaseURL != "" &&
		c.EmbeddingAPIKey != "" &&
		c.EmbeddingModel != nil && *c.EmbeddingModel != "" &&
		c.EmbeddingDims != nil && *c.EmbeddingDims > 0
}

// LLMConfig 状态常量
const (
	LLMConfigStatusNormal   = int8(0)
	LLMConfigStatusDisabled = int8(1)
)

// TableName 设置表名
func (LLMConfig) TableName() string {
	return "user_llm_configs"
}

// IsEnabled 配置是否启用
func (c *LLMConfig) IsEnabled() bool {
	return c.Status == LLMConfigStatusNormal && c.APIKey != ""
}

// GetBaseURL 获取实际使用的 API 地址
func (c *LLMConfig) GetBaseURL() string {
	if c.BaseURL == nil || *c.BaseURL == "" {
		return c.getDefaultBaseURL()
	}
	return *c.BaseURL
}

// getDefaultBaseURL 根据服务商返回默认 API 地址
func (c *LLMConfig) getDefaultBaseURL() string {
	switch c.Provider {
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "qwen", "aliyun_qwen":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case "doubao", "volcengine":
		return "https://ark.cn-beijing.volces.com/api/v3"
	case "baidu_qianfan":
		return "https://qianfan.baidubce.com/v2"
	case "azure", "custom_openai_compatible":
		return "" // 需要用户自定义地址
	default:
		return "https://api.openai.com/v1"
	}
}

// GetModel 获取实际使用的模型名
func (c *LLMConfig) GetModel() string {
	if c.Model == nil || *c.Model == "" {
		return c.getDefaultModel()
	}
	return *c.Model
}

// getDefaultModel 根据服务商返回默认模型
func (c *LLMConfig) getDefaultModel() string {
	switch c.Provider {
	case "anthropic":
		return "claude-3-sonnet-20240229"
	case "qwen":
		return "qwen-turbo"
	case "aliyun_qwen":
		return "qwen-plus"
	case "doubao":
		return "doubao-pro-32k"
	case "volcengine":
		return "doubao-seed-1-6"
	case "baidu_qianfan":
		return "ernie-4.0-turbo-8k"
	case "azure", "custom_openai_compatible":
		return "" // 需要用户自定义
	default:
		return "gpt-4o-mini"
	}
}

// GetTemperature 获取温度参数
func (c *LLMConfig) GetTemperature(defaultVal float64) float64 {
	if c.Temperature == nil {
		return defaultVal
	}
	return *c.Temperature
}

// GetMaxTokens 获取最大 Token 数
func (c *LLMConfig) GetMaxTokens(defaultVal int) int {
	if c.MaxTokens == nil || *c.MaxTokens <= 0 {
		return defaultVal
	}
	return *c.MaxTokens
}
