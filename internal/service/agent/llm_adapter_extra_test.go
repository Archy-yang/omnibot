package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// buildAgentRequest 快模式(M5/C):disableThinking 时请求体带 thinking.type=disabled;
// 默认不传 thinking 字段(模型默认行为)。
func TestBuildAgentRequest_ThinkingToggle(t *testing.T) {
	defaultClient := NewOpenAILLMClient("k", "http://x", "m", 0)
	body, _ := json.Marshal(defaultClient.buildAgentRequest(nil, nil, false))
	if strings.Contains(string(body), "thinking") {
		t.Errorf("默认请求不应带 thinking 字段: %s", body)
	}

	fastClient := NewOpenAILLMClient("k", "http://x", "m", 0)
	fastClient.SetDisableThinking(true)
	body2, _ := json.Marshal(fastClient.buildAgentRequest(nil, nil, true))
	var parsed struct {
		Thinking *struct {
			Type string `json:"type"`
		} `json:"thinking"`
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body2, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Thinking == nil || parsed.Thinking.Type != "disabled" {
		t.Errorf("快模式应带 thinking.type=disabled, got %s", body2)
	}
	if !parsed.Stream {
		t.Error("stream 标志应透传")
	}
}
