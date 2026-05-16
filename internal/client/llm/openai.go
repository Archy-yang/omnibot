package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// OpenAIProvider OpenAI API 兼容大模型提供者
// 支持 OpenAI 官方、Azure OpenAI、以及所有兼容 OpenAI API 协议的服务
type OpenAIProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// NewOpenAIProvider 创建 OpenAI 兼容 Provider
// apiKey: API 密钥
// baseURL: API 基础地址（如 https://api.openai.com/v1）
// model: 模型名称（如 gpt-3.5-turbo）
// timeout: 请求超时时间
func NewOpenAIProvider(apiKey, baseURL, model string, timeout time.Duration) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// openAIRequest OpenAI API 请求结构
type openAIRequest struct {
	Model    string        `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// openAIMessage 消息结构
type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openAIResponse OpenAI API 响应结构
type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Error   *openAIError   `json:"error,omitempty"`
}

// openAIChoice 响应选择
type openAIChoice struct {
	Message openAIMessage `json:"message"`
}

// openAIError 错误响应结构
type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// ChatCompletion 对话补全实现
func (p *OpenAIProvider) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error) {
	// 转换消息格式
	oaiMessages := make([]openAIMessage, len(messages))
	for i, msg := range messages {
		oaiMessages[i] = openAIMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	// 构造请求
	reqBody := openAIRequest{
		Model:    p.model,
		Messages: oaiMessages,
		Stream:   false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// 创建 HTTP 请求
	url := fmt.Sprintf("%s/chat/completions", p.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// 设置 Headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.apiKey))

	// 发送请求
	start := time.Now()
	resp, err := p.client.Do(req)
	if err != nil {
		logger.WarnWithFields("OpenAI API request failed",
			zap.String("error", err.Error()),
			zap.Duration("duration", time.Since(start)),
		)
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 解析响应
	var oaiResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&oaiResp); err != nil {
		logger.WarnWithFields("Failed to decode OpenAI response",
			zap.String("error", err.Error()),
			zap.Int("status_code", resp.StatusCode),
		)
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// 检查错误响应
	if oaiResp.Error != nil {
		logger.WarnWithFields("OpenAI API returned error",
			zap.String("error_type", oaiResp.Error.Type),
			zap.String("error_message", oaiResp.Error.Message),
			zap.String("error_code", oaiResp.Error.Code),
			zap.Int("status_code", resp.StatusCode),
		)
		return "", fmt.Errorf("API error: %s", oaiResp.Error.Message)
	}

	// 提取结果
	result := p.parseResponse(&oaiResp)

	logger.InfoWithFields("OpenAI API call succeeded",
		zap.String("model", p.model),
		zap.Duration("duration", time.Since(start)),
	)

	return result, nil
}

// parseResponse 解析响应并提取内容
func (p *OpenAIProvider) parseResponse(resp *openAIResponse) string {
	if len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content
	}
	return ""
}
