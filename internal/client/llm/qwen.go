package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"wechat-intelligent-bot/pkg/logger"

	"go.uber.org/zap"
)

// QwenProvider 阿里通义千问客户端
type QwenProvider struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewQwenProvider 创建通义千问客户端
func NewQwenProvider(apiKey, model string, timeout time.Duration) *QwenProvider {
	return &QwenProvider{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// qwenRequest 通义千问请求
type qwenRequest struct {
	Model    string        `json:"model"`
	Input    qwenInput     `json:"input"`
	Settings qwenSettings `json:"parameters,omitempty"`
}

type qwenInput struct {
	Messages []qwenMessage `json:"messages"`
}

type qwenMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type qwenSettings struct {
	ResultFormat string `json:"result_format,omitempty"`
}

// qwenResponse 通义千问响应
type qwenResponse struct {
	RequestID string         `json:"request_id"`
	Output    qwenOutput      `json:"output"`
	Usage     qwenUsage       `json:"usage"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
}

type qwenOutput struct {
	Text         string        `json:"text"`
	FinishReason string        `json:"finish_reason"`
}

type qwenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ChatCompletion 实现 LLMProvider 接口
func (p *QwenProvider) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error) {
	// 转换消息格式
	qwenMessages := make([]qwenMessage, len(messages))
	for i, msg := range messages {
		// 通义千问角色: system/user/assistant
		qwenMessages[i] = qwenMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	// 构建请求
	req := qwenRequest{
		Model: p.model,
		Input: qwenInput{
			Messages: qwenMessages,
		},
		Settings: qwenSettings{
			ResultFormat: "message",
		},
	}

	// 序列化
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	// 创建 HTTP 请求
	url := "https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	// 设置头
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	// 发送请求
	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	// 解析响应
	var qwenResp qwenResponse
	if err := json.NewDecoder(resp.Body).Decode(&qwenResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	// 检查错误
	if qwenResp.Code != "" {
		return "", fmt.Errorf("api error: %s - %s", qwenResp.Code, qwenResp.Message)
	}

	logger.InfoWithFields("Qwen API call success",
		zap.String("request_id", qwenResp.RequestID),
		zap.Int("total_tokens", qwenResp.Usage.TotalTokens),
	)

	return qwenResp.Output.Text, nil
}
