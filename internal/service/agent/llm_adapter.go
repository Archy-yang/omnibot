package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OpenAILLMClient 基于 OpenAI 协议的 LLM 客户端
// 直接构造 HTTP 请求以支持 tools 参数
type OpenAILLMClient struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// NewOpenAILLMClient 创建 Agent 专用 LLM 客户端
func NewOpenAILLMClient(apiKey, baseURL, model string, timeout time.Duration) *OpenAILLMClient {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAILLMClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		client:  &http.Client{Timeout: timeout},
	}
}

// agentRequest OpenAI chat completions request (includes tools)
type agentRequest struct {
	Model    string                   `json:"model"`
	Messages []map[string]interface{} `json:"messages"`
	Tools    []map[string]interface{} `json:"tools,omitempty"`
	Stream   bool                     `json:"stream"`
}

// agentResponse OpenAI chat completions response
type agentResponse struct {
	Choices []struct {
		Message struct {
			Content   string                   `json:"content"`
			ToolCalls []map[string]interface{} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ChatCompletion 实现 LLMClient 接口
func (c *OpenAILLMClient) ChatCompletion(ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{}) (string, []map[string]interface{}, error) {
	reqBody := agentRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
		Stream:   false,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	resp, err := c.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read response: %w", err)
	}

	var agentResp agentResponse
	if err := json.Unmarshal(body, &agentResp); err != nil {
		return "", nil, fmt.Errorf("decode response: %w", err)
	}

	if agentResp.Error != nil {
		return "", nil, fmt.Errorf("API error: %s", agentResp.Error.Message)
	}

	if len(agentResp.Choices) == 0 {
		return "", nil, fmt.Errorf("no choices in response")
	}

	choice := agentResp.Choices[0]
	return choice.Message.Content, choice.Message.ToolCalls, nil
}
