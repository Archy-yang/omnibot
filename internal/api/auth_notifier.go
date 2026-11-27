package api

// auth_notifier.go — 授权结果回推 IM(v2.13)。
//
// 渠道无关:只依赖 channelfactory 里注册的 MessageChannel(都实现了 SendText),
// 因此飞书 / 微信 ClawBot / 未来新渠道都能直接复用,不需要每个渠道写一个 notifier。
//
// 送达顺序:
//  1. 发起授权时记录的 channel + target(最精确,用户从哪问就回哪)
//  2. 回落到 user_channels 里第一个可异步发送的绑定
//  3. 都没有 → 静默返回 nil(状态已落库,历史可见即"不丢")

import (
	"context"
	"fmt"

	channelfactory "omnibot/internal/channel"
	userdomain "omnibot/internal/domain/user"
)

// userChannelLister 只需要"按用户查绑定"这一个能力(接口最小化,便于单测)。
// *userrepo.GormUserChannelRepository 满足它。
type userChannelLister interface {
	GetByUserID(userID int64) ([]*userdomain.UserChannel, error)
}

// channelAuthNotifier 实现 larkauth.Notifier。
type channelAuthNotifier struct {
	channels userChannelLister
}

func newChannelAuthNotifier(channels userChannelLister) *channelAuthNotifier {
	return &channelAuthNotifier{channels: channels}
}

// NotifyAuth 把授权结果发到用户所在的 IM 会话。
func (n *channelAuthNotifier) NotifyAuth(ctx context.Context, userID int64, channel, target, text string) error {
	if text == "" {
		return nil
	}
	_ = ctx // MessageChannel.SendText 不带 ctx(各渠道自己控超时)

	// Web 发起的授权不推 IM:Web 设置页自己会轮询状态,再往飞书/微信发一条
	// "授权好了"只会让用户困惑"我在网页点的,怎么微信响了"。
	if channel == "web" {
		return nil
	}

	if channel != "" && target != "" {
		if ch, ok := channelfactory.Get(channel); ok && ch.IsAsync() {
			if err := ch.SendText(target, text); err != nil {
				return fmt.Errorf("推送授权结果到 %s 失败: %w", channel, err)
			}
			return nil
		}
	}

	if n.channels == nil {
		return nil
	}
	bound, err := n.channels.GetByUserID(userID)
	if err != nil {
		return fmt.Errorf("查用户 %d 渠道绑定: %w", userID, err)
	}
	for _, c := range bound {
		ch, ok := channelfactory.Get(c.ChannelType)
		if !ok || !ch.IsAsync() {
			continue
		}
		if err := ch.SendText(c.ChannelUserID, text); err != nil {
			// 某个渠道发不出去不影响其它渠道
			continue
		}
		return nil
	}
	return nil
}
