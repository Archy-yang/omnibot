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

	"omnibot/pkg/logger"

	"go.uber.org/zap"
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

// 限流重试:LLM 提供方 429 / TPM/RPM 限流是瞬态错误,指数退避重试而非直接让整个 Agent 任务失败。
// 子 Agent 后台跑,重试等待可接受;主 Agent 受益但会多等几秒。仅对限流类错误重试,其他错误立即返回。
const rateLimitMaxRetries = 3

// rateLimitBaseDelay 用 var(非 const)便于测试调小加速重试;生产为 1s。
var rateLimitBaseDelay = 1 * time.Second

// isRateLimitError 判断是否限流类可重试错误:HTTP 429,或错误文案含 rate limit/TPM/RPM/quota。
func isRateLimitError(statusCode int, err error) bool {
	if statusCode == 429 {
		return true
	}
	if err != nil {
		msg := strings.ToLower(err.Error())
		return strings.Contains(msg, "rate limit") || strings.Contains(msg, "tpm") ||
			strings.Contains(msg, "rpm") || strings.Contains(msg, "quota")
	}
	return false
}

// rateLimitSleep 限流退避等待:第 attempt 次重试前等 baseDelay * 2^attempt(1s/2s/4s)。
// 返回 false 表示已达重试上限不再等待。等待期间响应 ctx 取消。
func rateLimitSleep(ctx context.Context, attempt int) bool {
	if attempt >= rateLimitMaxRetries {
		return false
	}
	delay := rateLimitBaseDelay * (1 << attempt)
	logger.WarnWithFields("llm rate limited, backing off before retry",
		zap.Int("attempt", attempt+1),
		zap.Duration("delay", delay),
	)
	select {
	case <-time.After(delay):
		return true
	case <-ctx.Done():
		return false
	}
}

// ChatCompletion 实现 LLMClient 接口。限流(429/TPM)时指数退避重试,避免单次限流让整个任务失败。
func (c *OpenAILLMClient) ChatCompletion(ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{}) (string, []map[string]interface{}, error) {
	jsonBody, err := json.Marshal(agentRequest{
		Model: c.model, Messages: messages, Tools: tools, Stream: false,
	})
	if err != nil {
		return "", nil, fmt.Errorf("marshal request: %w", err)
	}
	url := fmt.Sprintf("%s/chat/completions", c.baseURL)

	for attempt := 0; ; attempt++ {
		content, toolCalls, status, callErr := c.chatCompletionOnce(ctx, url, jsonBody)
		if callErr == nil {
			return content, toolCalls, nil
		}
		if !isRateLimitError(status, callErr) || !rateLimitSleep(ctx, attempt) {
			return "", nil, callErr
		}
	}
}

// chatCompletionOnce 执行一次非流式 chat completion 请求。返回 httpStatus 供上层判断限流(0=未拿到响应)。
func (c *OpenAILLMClient) chatCompletionOnce(ctx context.Context, url string, jsonBody []byte) (string, []map[string]interface{}, int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

	resp, err := c.client.Do(req)
	if err != nil {
		return "", nil, 0, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
	}

	var agentResp agentResponse
	if err := json.Unmarshal(body, &agentResp); err != nil {
		return "", nil, resp.StatusCode, fmt.Errorf("decode response: %w", err)
	}
	if agentResp.Error != nil {
		return "", nil, resp.StatusCode, fmt.Errorf("API error: %s", agentResp.Error.Message)
	}
	if len(agentResp.Choices) == 0 {
		return "", nil, resp.StatusCode, fmt.Errorf("no choices in response")
	}
	choice := agentResp.Choices[0]
	return choice.Message.Content, choice.Message.ToolCalls, resp.StatusCode, nil
}

// streamingChunk 对应 OpenAI SSE 流中一行 `data: {...}` 的 JSON 结构。
// 与非流式 agentResponse 的差异：流式响应每个 choice 用 delta 而非 message，
// 且 finish_reason 在最后一个 chunk 才出现。
type streamingChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"` // deepseek 思考模式:思考增量,千帆要求多轮回传
			ToolCalls        []struct {
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
	// 限流(429/TPM)时指数退避重试流打开;流已开始后的错误不重试(避免重复输出)。
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.apiKey))

		r, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}
		if r.StatusCode < 200 || r.StatusCode >= 300 {
			// 同步把 body 读完，给上层一个能定位问题的错误信息
			body, _ := io.ReadAll(r.Body)
			_ = r.Body.Close()
			statusErr := fmt.Errorf("API error %d: %s", r.StatusCode, strings.TrimSpace(string(body)))
			if isRateLimitError(r.StatusCode, statusErr) && rateLimitSleep(ctx, attempt) {
				continue
			}
			return nil, statusErr
		}
		resp = r
		break
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
			if choice.Delta.ReasoningContent != "" {
				out <- LLMStreamChunk{ReasoningDelta: choice.Delta.ReasoningContent}
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
