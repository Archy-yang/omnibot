package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainChunks 收集 chunk channel 的全部内容（带超时兜底，避免测试 hang）。
func drainChunks(t *testing.T, ch <-chan LLMStreamChunk) []LLMStreamChunk {
	t.Helper()
	var chunks []LLMStreamChunk
	timeout := time.After(2 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return chunks
			}
			chunks = append(chunks, c)
		case <-timeout:
			t.Fatal("timed out waiting for chunk channel close")
			return chunks
		}
	}
}

// streamingHTTPServer 起一个 SSE 服务器，把指定的字符串作为 body 一次性写入。
// 真实 OpenAI 是逐 token 推送，但 net/http 默认会立即 flush 每个 Write，
// 测试里整体一把发也能正确触发 SSE 解析（chunk 边界由换行决定）。
func streamingHTTPServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
}

// TestOpenAILLMClient_Stream_PureContent：简单文本流，无工具调用。
// 验证多个 content delta 被逐个推到 channel，[DONE] 触发 Done=true。
func TestOpenAILLMClient_Stream_PureContent(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"你"},"index":0}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"好"},"index":0}]}`,
		``,
		`data: {"choices":[{"delta":{"content":"！"},"index":0}]}`,
		``,
		`data: {"choices":[{"finish_reason":"stop","index":0,"delta":{}}]}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")

	srv := streamingHTTPServer(t, body)
	defer srv.Close()

	client := NewOpenAILLMClient("test-key", srv.URL, "gpt-test", 5*time.Second)

	ch, err := client.ChatCompletionStream(
		context.Background(),
		[]map[string]interface{}{{"role": "user", "content": "hi"}},
		nil,
	)
	require.NoError(t, err)

	chunks := drainChunks(t, ch)

	// 收集 content delta 拼接结果
	var content string
	var sawFinish, sawDone bool
	for _, c := range chunks {
		if c.ContentDelta != "" {
			content += c.ContentDelta
		}
		if c.FinishReason != "" {
			sawFinish = true
			assert.Equal(t, "stop", c.FinishReason)
		}
		if c.Done {
			sawDone = true
		}
	}
	assert.Equal(t, "你好！", content)
	assert.True(t, sawFinish, "expected finish_reason chunk")
	assert.True(t, sawDone, "expected [DONE] chunk")
}

// TestOpenAILLMClient_Stream_ToolCallSplitArguments：工具调用 + arguments 跨 chunk 拼接。
// 这是 OpenAI 流式 tool_calls 最常见的形态，必须能正确累积。
func TestOpenAILLMClient_Stream_ToolCallSplitArguments(t *testing.T) {
	body := strings.Join([]string{
		// 第一个 chunk：声明 tool_call，带 ID 和 name，arguments 为空
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"get_time","arguments":""}}]},"index":0}]}`,
		``,
		// 后续 chunk：仅 arguments delta，没有 ID/name
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"x\""}}]},"index":0}]}`,
		``,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":1}"}}]},"index":0}]}`,
		``,
		`data: {"choices":[{"finish_reason":"tool_calls","index":0,"delta":{}}]}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")

	srv := streamingHTTPServer(t, body)
	defer srv.Close()

	client := NewOpenAILLMClient("test-key", srv.URL, "gpt-test", 5*time.Second)

	ch, err := client.ChatCompletionStream(context.Background(), nil, nil)
	require.NoError(t, err)

	chunks := drainChunks(t, ch)

	// 累积 tool_call 增量验证
	var id, name, argsAccum string
	var sawFinish, sawDone bool
	var sawNonToolError error
	for _, c := range chunks {
		if c.Error != nil {
			sawNonToolError = c.Error
		}
		if c.ToolCallDelta != nil {
			if c.ToolCallDelta.ID != "" {
				id = c.ToolCallDelta.ID
			}
			if c.ToolCallDelta.Name != "" {
				name = c.ToolCallDelta.Name
			}
			argsAccum += c.ToolCallDelta.ArgumentsDelta
		}
		if c.FinishReason != "" {
			sawFinish = true
			assert.Equal(t, "tool_calls", c.FinishReason)
		}
		if c.Done {
			sawDone = true
		}
	}

	require.NoError(t, sawNonToolError)
	assert.Equal(t, "call_abc", id)
	assert.Equal(t, "get_time", name)
	assert.Equal(t, `{"x":1}`, argsAccum)
	assert.True(t, sawFinish)
	assert.True(t, sawDone)
}

// TestOpenAILLMClient_Stream_APIError：服务端返回 4xx/5xx 时应通过 chunk.Error 反馈，
// 不应让 channel 静悄悄关闭让上层以为正常结束。
func TestOpenAILLMClient_Stream_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer srv.Close()

	client := NewOpenAILLMClient("bad-key", srv.URL, "gpt-test", 5*time.Second)
	ch, err := client.ChatCompletionStream(context.Background(), nil, nil)

	// HTTP 错误可能在 ChatCompletionStream 直接返回 err，也可能在 channel 第一个 chunk 给出。
	// 任一形式都接受，但必须能让上层感知到。
	if err != nil {
		assert.Contains(t, err.Error(), "401")
		return
	}
	require.NotNil(t, ch)
	chunks := drainChunks(t, ch)
	require.NotEmpty(t, chunks)

	hasError := false
	for _, c := range chunks {
		if c.Error != nil {
			hasError = true
			break
		}
	}
	assert.True(t, hasError, "expected an Error chunk for HTTP 401, got: %+v", chunks)
}

// TestOpenAILLMClient_Stream_BadJSON：服务端返回的 SSE 行 JSON 非法时，应作为错误传播，
// 不应让上层拿到一个看起来正常但内容为空的流。
func TestOpenAILLMClient_Stream_BadJSON(t *testing.T) {
	body := strings.Join([]string{
		`data: {malformed`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")

	srv := streamingHTTPServer(t, body)
	defer srv.Close()

	client := NewOpenAILLMClient("test-key", srv.URL, "gpt-test", 5*time.Second)
	ch, err := client.ChatCompletionStream(context.Background(), nil, nil)
	require.NoError(t, err)

	chunks := drainChunks(t, ch)

	hasError := false
	for _, c := range chunks {
		if c.Error != nil {
			hasError = true
			break
		}
	}
	assert.True(t, hasError, "expected JSON parse error to propagate, got chunks: %+v", chunks)
}

// TestOpenAILLMClient_Stream_RequestSetsStreamFlag：客户端必须把 stream:true 写到请求 body
// 里，否则 OpenAI 会按非流式返回，整套体验失效。这是协议级的硬约束，单独测一下。
func TestOpenAILLMClient_Stream_RequestSetsStreamFlag(t *testing.T) {
	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := NewOpenAILLMClient("test-key", srv.URL, "gpt-test", 5*time.Second)
	ch, err := client.ChatCompletionStream(context.Background(), nil, nil)
	require.NoError(t, err)
	drainChunks(t, ch)

	assert.Contains(t, receivedBody, `"stream":true`)
}
