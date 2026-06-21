package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// streamingChunk 对应 OpenAI SSE 流中一行 `data: {...}` 的 JSON 结构。
// 与非流式 agentResponse 的差异：流式响应每个 choice 用 delta 而非 message，
// 且 finish_reason 在最后一个 chunk 才出现。
type streamingChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ChatCompletionStream 实现 StreamingLLMClient 接口。
// 内部起一个 goroutine 读 SSE 流，按行解析后转 LLMStreamChunk 推到 channel；
// 调用方仅需 range 消费，channel 由本方法保证关闭。
//
// 重要协议要点：
//   - body 必须包含 stream:true，否则部分 OpenAI 兼容服务商（如 Kimi、千帆）会
//     直接按非流式返回，破坏整个体验
//   - 每行 SSE 数据形如 `data: {...}` 或 `data: [DONE]`，行间用空行分隔
//   - HTTP 状态码非 2xx 时把 body 作为 error 抛出（注意 body 可能是 JSON 错误对象）
func (c *OpenAILLMClient) ChatCompletionStream(
	ctx context.Context,
	messages []map[string]interface{},
	tools []map[string]interface{},
) (<-chan LLMStreamChunk, error) {
	reqBody := agentRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/chat/completions", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 同步把 body 读完，给上层一个能定位问题的错误信息
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	out := make(chan LLMStreamChunk, 16)
	go func() {
		defer close(out)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		// SSE 单行可能很长（OpenAI tool_call arguments 不分片时可能上千字节），把缓冲调大
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				out <- LLMStreamChunk{Error: ctx.Err()}
				return
			default:
			}

			line := scanner.Text()
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue // 忽略非 data 字段（如 :ping 心跳）
			}
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				out <- LLMStreamChunk{Done: true}
				return
			}

			var chunk streamingChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				out <- LLMStreamChunk{Error: fmt.Errorf("parse SSE chunk: %w", err)}
				return
			}
			if chunk.Error != nil {
				out <- LLMStreamChunk{Error: fmt.Errorf("API error: %s", chunk.Error.Message)}
				return
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]

			if choice.Delta.Content != "" {
				out <- LLMStreamChunk{ContentDelta: choice.Delta.Content}
			}
			for _, tc := range choice.Delta.ToolCalls {
				out <- LLMStreamChunk{
					ToolCallDelta: &ToolCallDelta{
						Index:          tc.Index,
						ID:             tc.ID,
						Name:           tc.Function.Name,
						ArgumentsDelta: tc.Function.Arguments,
					},
				}
			}
			if choice.FinishReason != "" {
				out <- LLMStreamChunk{FinishReason: choice.FinishReason}
			}
		}

		if err := scanner.Err(); err != nil {
			out <- LLMStreamChunk{Error: fmt.Errorf("read SSE stream: %w", err)}
		}
	}()

	return out, nil
}
