package channel

import (
	domainchannel "omnibot/internal/domain/channel"
)

var channels = make(map[string]domainchannel.MessageChannel)

// Register 注册通道实现
func Register(ch domainchannel.MessageChannel) {
	channels[ch.ChannelType()] = ch
}

// Get 获取指定类型的通道
func Get(channelType string) (domainchannel.MessageChannel, bool) {
	ch, ok := channels[channelType]
	return ch, ok
}

// List 返回所有已注册的通道类型
func List() []string {
	types := make([]string, 0, len(channels))
	for t := range channels {
		types = append(types, t)
	}
	return types
}
