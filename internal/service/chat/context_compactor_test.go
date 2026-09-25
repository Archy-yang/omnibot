package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"omnibot/internal/client/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubCompactLLM 记录请求的 LLM 桩。
type stubCompactLLM struct {
	lastMessages []llm.ChatMessage
	output       string
	err          error
}

func (s *stubCompactLLM) ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	s.lastMessages = messages
	return s.output, s.err
}

// TestCompactPrompt_GuardClauses prompt 守护:§7.2 工作集小节与关键要求丢失即报错。
func TestCompactPrompt_GuardClauses(t *testing.T) {
	for _, want := range []string{
		"<conversation_state>",
		"Current Topics", "Current Goals", "Decisions", "Rejected Approaches",
		"Constraints", "Open Questions", "Pending Work",
		"Task / Artifact References", "Current State",
		"保留未来继续对话所需要的信息", // §7.2 核心:不为缩短 token 牺牲细节
		"带时间点",
	} {
		if !strings.Contains(compactSystemPrompt, want) {
			t.Errorf("Compact prompt 缺少关键约束 %q", want)
		}
	}
}

// TestLLMContextCompactor_MergesOldAndNew 输入应包含旧工作集与全部区间原文。
func TestLLMContextCompactor_MergesOldAndNew(t *testing.T) {
	stub := &stubCompactLLM{output: "<conversation_state>...</conversation_state>"}
	compactor := NewLLMContextCompactor(nil, stub)

	out, err := compactor.Compact(context.Background(), 42, "旧工作集内容", []string{"[user] 问题A", "[assistant(子任务汇报)] 结论B"})
	require.NoError(t, err)
	assert.Equal(t, "<conversation_state>...</conversation_state>", out)

	require.Len(t, stub.lastMessages, 2)
	assert.Equal(t, "system", stub.lastMessages[0].Role)
	assert.Equal(t, "user", stub.lastMessages[1].Role)
	assert.Contains(t, stub.lastMessages[1].Content, "旧工作集内容")
	assert.Contains(t, stub.lastMessages[1].Content, "[user] 问题A")
	assert.Contains(t, stub.lastMessages[1].Content, "[assistant(子任务汇报)] 结论B")
}

// TestLLMContextCompactor_Failures 空输出/LLM 报错必须返回错误(调用方降级不推进水位)。
func TestLLMContextCompactor_Failures(t *testing.T) {
	compactor := NewLLMContextCompactor(nil, &stubCompactLLM{output: "  "})
	_, err := compactor.Compact(context.Background(), 42, "", []string{"[user] x"})
	assert.Error(t, err, "空输出应报错,防止水位推进到空 Compact")

	compactor = NewLLMContextCompactor(nil, &stubCompactLLM{err: errors.New("boom")})
	_, err = compactor.Compact(context.Background(), 42, "", []string{"[user] x"})
	assert.Error(t, err)
}

// TestLLMContextCompactor_EmptyRawTexts 无区间输入直接返回旧 Compact(不发 LLM 调用)。
func TestLLMContextCompactor_EmptyRawTexts(t *testing.T) {
	stub := &stubCompactLLM{output: "不应被调用"}
	compactor := NewLLMContextCompactor(nil, stub)
	out, err := compactor.Compact(context.Background(), 42, "旧工作集", nil)
	require.NoError(t, err)
	assert.Equal(t, "旧工作集", out)
	assert.Nil(t, stub.lastMessages)
}
