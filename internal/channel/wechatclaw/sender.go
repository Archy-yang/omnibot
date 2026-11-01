package wechatclaw

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// maxMessageRunes 单条文本上限(微信侧有长度限制,超长需分段)。
// 取 1800 留余量:官方插件也按段落切分发送。
const maxMessageRunes = 1800

// SenderClient 出站所需的协议能力(协议客户端的子集,便于测试替身)。
type SenderClient interface {
	SendText(ctx context.Context, toUserID, text, contextToken, clientID string) (string, error)
}

// TokenSource 按对端取 context_token(context_token 是回消息的会话锚点)。
type TokenSource interface {
	Token(peer string) string
}

// Sender 实现 im.Sender:把 Agent 输出按需分段后发到微信 ClawBot。
//
// ClawBot 不支持 markdown 渲染,SendMarkdown 等价 SendText;SendCard 把标题与正文拼接。
type Sender struct {
	client      SenderClient
	tokens      TokenSource
	newClientID func() string
}

// NewSender 创建发送器。
func NewSender(client SenderClient, tokens TokenSource) *Sender {
	return &Sender{
		client:      client,
		tokens:      tokens,
		newClientID: newClientID,
	}
}

// newClientID 生成出站消息 client_id(仅用于幂等/去重,官方插件同款前缀风格)。
func newClientID() string {
	return fmt.Sprintf("omnibot-wechatclaw-%d-%d", time.Now().UnixNano(), rand.Int63()) //#nosec G404 -- 非安全用途
}

// SendText 发送纯文本(超长自动分段)。
func (s *Sender) SendText(ctx context.Context, openID, content string) error {
	for _, part := range splitMessage(content) {
		if _, err := s.client.SendText(ctx, openID, part, s.tokens.Token(openID), s.newClientID()); err != nil {
			return err
		}
	}
	return nil
}

// SendMarkdown 微信不渲染 markdown,降级为纯文本。
func (s *Sender) SendMarkdown(ctx context.Context, openID, content string) error {
	return s.SendText(ctx, openID, content)
}

// SendCard 卡片降级为「标题 + 正文」文本。
func (s *Sender) SendCard(ctx context.Context, openID, title, content, _ string) error {
	if strings.TrimSpace(title) == "" {
		return s.SendText(ctx, openID, content)
	}
	return s.SendText(ctx, openID, title+"\n\n"+content)
}

// splitMessage 按 rune 长度切分,优先在换行处断开,避免把中文字符切坏。
func splitMessage(content string) []string {
	runes := []rune(content)
	if len(runes) <= maxMessageRunes {
		return []string{content}
	}

	var parts []string
	for len(runes) > 0 {
		if len(runes) <= maxMessageRunes {
			parts = append(parts, string(runes))
			break
		}
		// 在 [1/3, 1] 窗口内找最后一个换行,避免把一段切得过短
		cut := maxMessageRunes
		for i := maxMessageRunes - 1; i > maxMessageRunes/3; i-- {
			if runes[i] == '\n' {
				cut = i + 1
				break
			}
		}
		parts = append(parts, strings.TrimRight(string(runes[:cut]), "\n"))
		runes = runes[cut:]
	}
	return parts
}
