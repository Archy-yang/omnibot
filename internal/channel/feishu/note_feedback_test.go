package feishu

// 29号 N4:主动消息反馈文本通道测试——精确匹配防误伤,svc 未注入不拦。

import (
	"errors"
	"testing"

	butlerdomain "omnibot/internal/domain/butler"
	butlersvc "omnibot/internal/service/butler"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeNoteFeedbackSvc struct {
	userID   int64
	feedback string
	err      error
}

func (f *fakeNoteFeedbackSvc) FeedbackLatestDeliveredNote(userID int64, feedback string) (string, error) {
	f.userID, f.feedback = userID, feedback
	if f.err != nil {
		return "", f.err
	}
	return "好的,这类消息 30 天内不再主动提;想恢复随时说一声。", nil
}

func TestHandleNoteFeedback_Intents(t *testing.T) {
	svc := &fakeNoteFeedbackSvc{}
	h := newHandler(nil, nil, nil, nil, &mockSender{})
	h.SetNoteFeedbackService(svc)

	// positive 双指令
	for _, text := range []string{"有用", "赞"} {
		reply, handled := h.handleNoteFeedback(42, text)
		require.True(t, handled, text)
		assert.NotEmpty(t, reply)
		assert.Equal(t, butlerdomain.NoteFeedbackPositive, svc.feedback, text)
	}

	// negative 三指令
	for _, text := range []string{"不用再提", "别再提", "没用"} {
		reply, handled := h.handleNoteFeedback(42, text)
		require.True(t, handled, text)
		assert.Contains(t, reply, "30 天", text)
		assert.Equal(t, butlerdomain.NoteFeedbackNegative, svc.feedback, text)
	}
	assert.Equal(t, int64(42), svc.userID)

	// 普通文本不拦(防误伤正常对话)
	_, handled := h.handleNoteFeedback(42, "这个方案没用,换一个")
	assert.False(t, handled)
	_, handled = h.handleNoteFeedback(42, "今天天气不错")
	assert.False(t, handled)
}

func TestHandleNoteFeedback_Guards(t *testing.T) {
	// svc 未注入(管家未装配)→ 不拦,走正常对话
	h := newHandler(nil, nil, nil, nil, &mockSender{})
	_, handled := h.handleNoteFeedback(42, "不用再提")
	assert.False(t, handled)

	// 无可反馈记录 → 兜底文案,仍算拦下
	h2 := newHandler(nil, nil, nil, nil, &mockSender{})
	h2.SetNoteFeedbackService(&fakeNoteFeedbackSvc{err: butlersvc.ErrNoteNotFound})
	reply, handled := h2.handleNoteFeedback(42, "不用再提")
	require.True(t, handled)
	assert.Contains(t, reply, "没有可反馈")
	assert.True(t, errors.Is(butlersvc.ErrNoteNotFound, butlersvc.ErrNoteNotFound))
}
