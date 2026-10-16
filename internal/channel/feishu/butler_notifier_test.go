package feishu

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	userdomain "omnibot/internal/domain/user"
)

// stubChannelRepo 仅实现 butler_notifier 用到的 GetByUserID。
type stubChannelRepo struct {
	channels []*userdomain.UserChannel
	err      error
}

func (s *stubChannelRepo) Create(*userdomain.UserChannel) error { return nil }
func (s *stubChannelRepo) GetByID(int64) (*userdomain.UserChannel, error) {
	return nil, errors.New("not implemented")
}
func (s *stubChannelRepo) GetByChannel(string, string) (*userdomain.UserChannel, error) {
	return nil, errors.New("not implemented")
}
func (s *stubChannelRepo) GetByUserID(int64) ([]*userdomain.UserChannel, error) {
	return s.channels, s.err
}
func (s *stubChannelRepo) Update(*userdomain.UserChannel) error { return nil }
func (s *stubChannelRepo) Delete(int64) error                   { return nil }

func feishuChannel(openID string) *userdomain.UserChannel {
	return &userdomain.UserChannel{UserID: 1, ChannelType: "feishu", ChannelUserID: openID}
}

func TestButlerNotifier_SendsCardWithFeedbackTail(t *testing.T) {
	sender := &mockSender{}
	n := NewFeishuButlerNotifier(sender, &stubChannelRepo{channels: []*userdomain.UserChannel{feishuChannel("ou_abc")}})

	require.NoError(t, n.NotifyProactive(context.Background(), 1, "你的项目方案今天下午该交了"))
	assert.Equal(t, "card", sender.lastMode)
	assert.Equal(t, "ou_abc", sender.sentOpenID)
	assert.Equal(t, butlerCardTitle, sender.sentTitle)
	assert.Equal(t, butlerCardTemplate, sender.sentTemplate)
	assert.Contains(t, sender.sentContent, "项目方案")
	assert.Contains(t, sender.sentContent, "不用再提", "尾注必须带反馈提示")
}

func TestButlerNotifier_NoBinding_SilentNil(t *testing.T) {
	sender := &mockSender{}
	n := NewFeishuButlerNotifier(sender, &stubChannelRepo{channels: nil})

	require.NoError(t, n.NotifyProactive(context.Background(), 1, "消息"), "未绑定渠道=仅落库,静默不算错")
	assert.Zero(t, sender.sendCount, "不应发送")
}

func TestButlerNotifier_RepoError_ReturnsError(t *testing.T) {
	n := NewFeishuButlerNotifier(&mockSender{}, &stubChannelRepo{err: errors.New("db down")})
	require.Error(t, n.NotifyProactive(context.Background(), 1, "消息"))
}
