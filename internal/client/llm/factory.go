package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"omnibot/pkg/config"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// Client LLM 客户端管理器
// 管理默认厂商和降级列表，自动处理降级
type Client struct {
	defaultProvider    LLMProvider
	fallbackProviders []LLMProvider
}

// UserConfig 用户级 LLM 配置
type UserConfig struct {
	Provider string // 服务商：openai/anthropic/azure/qwen/doubao
	APIKey   string
	BaseURL  string
	Model    string
}

// NewClient 根据配置创建 LLM 客户端
func NewClient(cfg config.LLMConfig) (*Client, error) {
	// 创建所有 providers
	providers := make(map[string]LLMProvider)

	for name, providerCfg := range cfg.Providers {
		provider, err := createProvider(name, providerCfg)
		if err != nil {
			logger.WarnWithFields("Failed to create provider",
				zap.String("provider", name),
				zap.Error(err),
			)
			continue
		}
		providers[name] = provider
	}

	// 检查默认 provider
	defaultName := cfg.Routing.Default
	defaultProvider, ok := providers[defaultName]
	if !ok {
		return nil, fmt.Errorf("default provider '%s' not found in configured providers", defaultName)
	}

	// 收集 fallback providers
	var fallbackProviders []LLMProvider
	for _, name := range cfg.Routing.FallbackOrder {
		if provider, ok := providers[name]; ok && name != defaultName {
			fallbackProviders = append(fallbackProviders, provider)
		}
	}

	return &Client{
		defaultProvider:    defaultProvider,
		fallbackProviders: fallbackProviders,
	}, nil
}

// NewClientFromUserConfig 从用户级配置创建简化版 LLM 客户端
// 不包含 fallback 机制，只使用用户指定的单一配置
func NewClientFromUserConfig(cfg UserConfig) (*Client, error) {
	var provider LLMProvider
	timeout := 30 * time.Second

	switch strings.ToLower(cfg.Provider) {
	case "qwen", "tongyi", "alibabacloud":
		provider = NewQwenProvider(cfg.APIKey, cfg.Model, timeout)
	case "doubao", "bytedance", "volcengine":
		provider = NewDoubaoProvider(cfg.APIKey, cfg.Model, timeout)
	case "openai", "azure", "anthropic":
		provider = NewOpenAIProvider(cfg.APIKey, cfg.BaseURL, cfg.Model, timeout)
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", cfg.Provider)
	}

	return &Client{
		defaultProvider:    provider,
		fallbackProviders: nil, // 用户级配置不支持 fallback
	}, nil
}

// ChatCompletion 对话补全，自动降级处理
func (c *Client) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error) {
	// 先尝试默认 provider
	resp, err := c.defaultProvider.ChatCompletion(ctx, messages)
	if err == nil {
		return resp, nil
	}

	logger.WarnWithFields("Default provider failed, trying fallback",
		zap.String("error", err.Error()),
	)

	// 依次尝试 fallback
	for i, provider := range c.fallbackProviders {
		logger.InfoWithFields("Trying fallback provider",
			zap.Int("index", i),
		)

		resp, err := provider.ChatCompletion(ctx, messages)
		if err == nil {
			return resp, nil
		}

		logger.WarnWithFields("Fallback provider failed",
			zap.Int("index", i),
			zap.String("error", err.Error()),
		)
	}

	// 全部失败
	return "", errors.New("all providers failed")
}

// StreamChatCompletion 流式对话补全，不支持降级
func (c *Client) StreamChatCompletion(ctx context.Context, messages []ChatMessage) (<-chan StreamChunk, error) {
	return c.defaultProvider.StreamChatCompletion(ctx, messages)
}

// createProvider 根据名称创建具体 provider
func createProvider(name string, cfg config.ProviderConfig) (LLMProvider, error) {
	// 解析超时
	timeout, err := time.ParseDuration(cfg.Timeout)
	if err != nil {
		// 默认 30 秒
		timeout = 30 * time.Second
	}

	name = strings.ToLower(name)
	switch name {
	case "qwen", "tongyi", "alibabacloud":
		return NewQwenProvider(cfg.APIKey, cfg.Model, timeout), nil
	case "doubao", "bytedance", "volcengine":
		return NewDoubaoProvider(cfg.APIKey, cfg.Model, timeout), nil
	case "openai", "azure":
		return NewOpenAIProvider(cfg.APIKey, cfg.BaseURL, cfg.Model, timeout), nil
	default:
		return nil, fmt.Errorf("unknown provider type: %s", name)
	}
}
