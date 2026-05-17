// Package web implements the Web channel for OmniBot
package web

import (
	domainchannel "omnibot/internal/domain/channel"
)

// Ensure Channel implements MessageChannel interface
var _ domainchannel.MessageChannel = (*Channel)(nil)

// Channel is the Web message channel implementation
type Channel struct{}

// NewChannel creates a new Web channel
func NewChannel() *Channel {
	return &Channel{}
}

// ChannelType returns "web"
func (c *Channel) ChannelType() string {
	return "web"
}

// IsAsync returns true - Web supports async via WebSocket
func (c *Channel) IsAsync() bool {
	return true
}

// SendText sends a message to the user (no-op for now, HTTP response handles it)
func (c *Channel) SendText(channelUserID string, content string) error {
	// Web messages are returned synchronously in HTTP response
	// This is a no-op but implements the interface
	return nil
}

// SendReply replies to a specific message (no-op for now)
func (c *Channel) SendReply(channelMessageID string, channelUserID string, content string) error {
	// Same as above - response handled in HTTP layer
	return nil
}
