package feishu

// butler_notifier.go — 主动管家飞书送达(26 号 N2)。
// userID → user_channels 查 feishu open_id → SendCard;未绑定渠道静默返回 nil
// (设计红线:未绑定 → 仅落库,历史可见即"不丢",不算错误)。

import (
	"context"
	"fmt"

	userrepo "omnibot/internal/repository/user"

	"go.uber.org/zap"

	"omnibot/pkg/logger"
)

// 主动关心卡片:绿色温和色调,与任务汇报(蓝色)区分;尾注反馈提示(26 号 §4)。
const (
	butlerCardTitle    = "🌿 管家问候"
	butlerCardTemplate = "green"
	butlerFeedbackTail = "\n\n---\n有用的话回复「有用」;不需要再提回复「不用再提」,我从此不再说这件事。"
)

// FeishuButlerNotifier 主动管家的飞书送达器,实现 butler.Notifier。
type FeishuButlerNotifier struct {
	sender   Sender
	channels userrepo.UserChannelRepository
}

func NewFeishuButlerNotifier(sender Sender, channels userrepo.UserChannelRepository) *FeishuButlerNotifier {
	return &FeishuButlerNotifier{sender: sender, channels: channels}
}

// NotifyProactive 把主动消息推成飞书卡片。未绑定飞书渠道 → 静默 nil(仅落库即达)。
func (n *FeishuButlerNotifier) NotifyProactive(ctx context.Context, userID int64, content string) error {
	chs, err := n.channels.GetByUserID(userID)
	if err != nil {
		return fmt.Errorf("查用户 %d 渠道绑定: %w", userID, err)
	}
	var openID string
	for _, ch := range chs {
		if ch.ChannelType == "feishu" {
			openID = ch.ChannelUserID
			break
		}
	}
	if openID == "" {
		logger.InfoWithFields("butler: user has no feishu binding, deliver by db only",
			zap.Int64("user_id", userID))
		return nil
	}
	if err := n.sender.SendCard(ctx, openID, butlerCardTitle, content+butlerFeedbackTail, butlerCardTemplate); err != nil {
		return fmt.Errorf("发送主动关心卡片: %w", err)
	}
	logger.InfoWithFields("butler: proactive card sent",
		zap.Int64("user_id", userID), zap.String("open_id", openID))
	return nil
}
