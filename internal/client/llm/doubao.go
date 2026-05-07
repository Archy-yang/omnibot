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

// DoubaoProvider 字节豆包客户端
type DoubaoProvider struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewDoubaoProvider 创建字节豆包客户端
func NewDoubaoProvider(apiKey, model string, timeout time.Duration) *DoubaoProvider {
	return &DoubaoProvider{
		apiKey: apiKey,
		model:  model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// doubaoRequest 豆包请求
type doubaoRequest struct {
	Model    string          `json:"model"`
	Messages []doubaoMessage `json:"messages"`
}

type doubaoMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// doubaoResponse 豆包响应
type doubaoResponse struct {
	ID      string         `json:"id"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Object  string         `json:"object"`
	Choices []doubaoChoice `json:"choices"`
	Usage   doubaoUsage    `json:"usage"`
	Error   *doubaoError   `json:"error"`
}

type doubaoChoice struct {
	Index        int             `json:"index"`
	Message      doubaoMessage   `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type doubaoUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type doubaoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ChatCompletion 实现 LLMProvider 接口
func (p *DoubaoProvider) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error) {
	// 转换消息格式
	doubaoMessages := make([]doubaoMessage, len(messages))
	for i, msg := range messages {
		// 豆包角色: system/user/assistant
		doubaoMessages[i] = doubaoMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	// 构建请求
	req := doubaoRequest{
		Model:    p.model,
		Messages: doubaoMessages,
	}

	// 序列化
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	// 创建 HTTP 请求 - 火山引擎豆包
	url := "https://ark.cn-beijing.volces.com/api/v3/chat/completions"
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
	var doubaoResp doubaoResponse
	if err := json.NewDecoder(resp.Body).Decode(&doubaoResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	// 检查错误
	if doubaoResp.Error != nil {
		return "", fmt.Errorf("api error: %s - %s", doubaoResp.Error.Code, doubaoResp.Error.Message)
	}

	// 提取回复
	if len(doubaoResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	logger.InfoWithFields("Doubao API call success",
		zap.String("request_id", doubaoResp.ID),
		zap.Int("total_tokens", doubaoResp.Usage.TotalTokens),
	)

	return doubaoResp.Choices[0].Message.Content, nil
}
