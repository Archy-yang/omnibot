package channel

import domainchannel "omnibot/internal/domain/channel"

var channels = make(map[string]domainchannel.MessageChannel)

// Register 注册渠道
func Register(ch domainchannel.MessageChannel) {
	channels[ch.ChannelType()] = ch
}

// Get 获取渠道
func Get(channelType string) (domainchannel.MessageChannel, bool) {
	ch, ok := channels[channelType]
	return ch, ok
}

// List 获取所有已注册的渠道类型
func List() []string {
	result := make([]string, 0, len(channels))
	for typ := range channels {
		result = append(result, typ)
	}
	return result
}
