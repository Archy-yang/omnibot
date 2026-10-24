package feishu

// note_feedback.go — 主动消息反馈文本通道(29号 N4)。
// 卡片按钮回调被飞书 Go SDK ws 长连接阻断(全版本丢弃 MessageTypeCard 帧,
// WithCardHandler 至今注释),文本指令是可落地的反馈形态,同时兑现 26号
// 卡片尾注「回复『不用再提』」的承诺。红线:精确匹配防误伤正常对话。

import (
	butlerdomain "omnibot/internal/domain/butler"
)

// feedbackIntentMap 送达反馈文本意图(精确匹配)。
var feedbackIntentMap = map[string]string{
	"有用":   butlerdomain.NoteFeedbackPositive,
	"赞":    butlerdomain.NoteFeedbackPositive,
	"不用再提": butlerdomain.NoteFeedbackNegative,
	"别再提":  butlerdomain.NoteFeedbackNegative,
	"没用":   butlerdomain.NoteFeedbackNegative,
}

// NoteFeedbackService 主动消息反馈窄面(service/butler SettingsService 实现)。
type NoteFeedbackService interface {
	// FeedbackLatestDeliveredNote 反馈落到该用户最近一条 delivered 主动消息,返回确认文案。
	FeedbackLatestDeliveredNote(userID int64, feedback string) (string, error)
}

// handleNoteFeedback 反馈指令钩子:命中且 svc 已装配 → 返回确认文案并拦下(不进对话);
// 未命中/未装配 → 不拦。ErrNoteNotFound 等失败给兜底文案(仍拦下——意图已明确表达)。
func (h *MessageHandler) handleNoteFeedback(userID int64, text string) (string, bool) {
	if h.noteFeedbackSvc == nil {
		return "", false
	}
	fb, ok := feedbackIntentMap[text]
	if !ok {
		return "", false
	}
	reply, err := h.noteFeedbackSvc.FeedbackLatestDeliveredNote(userID, fb)
	if err != nil {
		return "最近没有可反馈的主动消息。", true
	}
	return reply, true
}
