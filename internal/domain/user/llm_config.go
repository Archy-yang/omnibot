package user

import (
	"time"
)

// LLMConfig 用户自定义 LLM 配置
type LLMConfig struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	UserID    int64     `gorm:"not null;uniqueIndex"`
	APIKey    string    `gorm:"size:512;not null"` // 加密后存储
	BaseURL   *string   `gorm:"size:256"`
	Model     *string   `gorm:"size:128"`
	Status    int8      `gorm:"default:0;not null"` // 0-正常, 1-禁用
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
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
		return "https://api.openai.com/v1"
	}
	return *c.BaseURL
}

// GetModel 获取实际使用的模型名
func (c *LLMConfig) GetModel() string {
	if c.Model == nil || *c.Model == "" {
		return "gpt-3.5-turbo"
	}
	return *c.Model
}
