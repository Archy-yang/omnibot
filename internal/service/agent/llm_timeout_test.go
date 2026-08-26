package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowStreamingServer 起一个 SSE 服务器:按 tokens 逐步写(每块之间 gap),模拟慢速但持续流式。
// 用于验证"读空闲超时"而非"总超时":只要块间隔 < readIdle,即使累计时长超限也不应被掐。
func slowStreamingServer(t *testing.T, tokens []string, gap time.Duration) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		for _, tkn := range tokens {
			_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"`+tkn+`"},"index":0}]}`+"\n\n")
			f.Flush()
			time.Sleep(gap)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		f.Flush()
	}))
}

// TestOpenAILLMClient_Stream_FlowingLongNotKilledByTotal 流式核心:readIdle(块间隔)未超界时,
// 累计时长(readIdle * 块数)超过单请求 timeout 也不应中断——证明"读空闲超时"而非"总超时"。
//
// timeout=300ms, 3 块各间隔 200ms -> 累计 600ms > 300ms,但每块间隔 200ms < 300ms。
// 旧实现(http.Client.Timeout=300ms)会在累计 300ms 掐断,只收到前 2 块;新实现应完整收齐。
func TestOpenAILLMClient_Stream_FlowingLongNotKilledByTotal(t *testing.T) {
	srv := slowStreamingServer(t, []string{"你", "好", "呀"}, 200*time.Millisecond)
	defer srv.Close()

	client := NewOpenAILLMClient("k", srv.URL, "m", 300*time.Millisecond)
	ch, err := client.ChatCompletionStream(context.Background(), nil, nil)
	require.NoError(t, err)

	chunks := drainChunks(t, ch)

	var content strings.Builder
	for _, c := range chunks {
		if c.ContentDelta != "" {
			content.WriteString(c.ContentDelta)
		}
	}
	assert.Equal(t, "你好呀", content.String(), "流式持续输出不应因累计超时被掐,应完整送达")
}

// TestOpenAILLMClient_Stream_ReadIdleTimeout 读空闲超时:服务器发一块后静默超过 readIdle,
// 客户端应报"读超时"错误(而不是一直等或按总长的语义误判)。
func TestOpenAILLMClient_Stream_ReadIdleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"我"},"index":0}]}`+"\n\n")
		f.Flush()
		// 发一块后静默 2s 再 DONE;readIdle=300ms 应先触发读超时错误
		time.Sleep(2 * time.Second)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		f.Flush()
	}))
	defer srv.Close()

	client := NewOpenAILLMClient("k", srv.URL, "m", 300*time.Millisecond)
	ch, err := client.ChatCompletionStream(context.Background(), nil, nil)
	require.NoError(t, err)

	chunks := drainChunks(t, ch)

	var sawErr bool
	var got string
	for _, c := range chunks {
		if c.Error != nil {
			sawErr = true
		}
		if c.ContentDelta != "" {
			got += c.ContentDelta
		}
	}
	assert.Equal(t, "我", got, "超时前收到的那一块应保留")
	assert.True(t, sawErr, "跨块静默 > readIdle 应报读超时错误")
}
