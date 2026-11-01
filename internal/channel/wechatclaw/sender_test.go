package wechatclaw

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSenderClient 记录出站调用。
type fakeSenderClient struct {
	calls []struct {
		to, text, ctxToken, clientID string
	}
}

func (f *fakeSenderClient) SendText(ctx context.Context, to, text, ctxToken, clientID string) (string, error) {
	f.calls = append(f.calls, struct {
		to, text, ctxToken, clientID string
	}{to, text, ctxToken, clientID})
	return "id", nil
}

type staticTokens map[string]string

func (s staticTokens) Token(peer string) string { return s[peer] }

func TestSenderPassesContextToken(t *testing.T) {
	client := &fakeSenderClient{}
	sender := NewSender(client, staticTokens{"peer-1": "ctx-1"})
	sender.newClientID = func() string { return "cid-1" }

	require.NoError(t, sender.SendText(context.Background(), "peer-1", "hi"))
	require.Len(t, client.calls, 1)
	assert.Equal(t, "peer-1", client.calls[0].to)
	assert.Equal(t, "hi", client.calls[0].text)
	assert.Equal(t, "ctx-1", client.calls[0].ctxToken)
	assert.Equal(t, "cid-1", client.calls[0].clientID)
}

func TestSenderSplitsLongMessage(t *testing.T) {
	client := &fakeSenderClient{}
	sender := NewSender(client, staticTokens{})

	long := strings.Repeat("字", maxMessageRunes+100)
	require.NoError(t, sender.SendText(context.Background(), "peer-1", long))

	require.Len(t, client.calls, 2)
	total := 0
	for _, c := range client.calls {
		total += len([]rune(c.text))
	}
	assert.Equal(t, maxMessageRunes+100, total)
}

func TestSenderSplitsOnNewline(t *testing.T) {
	client := &fakeSenderClient{}
	sender := NewSender(client, staticTokens{})

	// 构造「每 100 字一行」的超长文本,期望在换行处断开而不是硬切
	line := strings.Repeat("a", 100)
	long := strings.Repeat(line+"\n", 25) // 2525 字

	parts := splitMessage(long)
	require.Greater(t, len(parts), 1)
	for _, p := range parts {
		assert.LessOrEqual(t, len([]rune(p)), maxMessageRunes)
	}
	// 拼回去等于原文(仅去掉了分段处的换行)
	assert.Equal(t, strings.ReplaceAll(long, "\n", ""), strings.ReplaceAll(strings.Join(parts, ""), "\n", ""))

	require.NoError(t, sender.SendText(context.Background(), "peer-1", long))
	assert.Len(t, client.calls, len(parts))
}

func TestSenderCardAndMarkdown(t *testing.T) {
	client := &fakeSenderClient{}
	sender := NewSender(client, staticTokens{})

	require.NoError(t, sender.SendMarkdown(context.Background(), "peer-1", "**加粗**"))
	require.NoError(t, sender.SendCard(context.Background(), "peer-1", "标题", "正文", "blue"))

	require.Len(t, client.calls, 2)
	assert.Equal(t, "**加粗**", client.calls[0].text)
	assert.Equal(t, "标题\n\n正文", client.calls[1].text)
}

func TestChannelWithoutSender(t *testing.T) {
	ch := NewChannel(func() *Sender { return nil })
	assert.True(t, ch.IsAsync())
	assert.Equal(t, ChannelType, ch.ChannelType())
	assert.ErrorIs(t, ch.SendText("peer-1", "hi"), ErrNotConnected)
}

func TestChannelSendTextWithSender(t *testing.T) {
	client := &fakeSenderClient{}
	sender := NewSender(client, staticTokens{"peer-1": "ctx-1"})
	sender.newClientID = func() string { return "cid" }
	ch := NewChannel(func() *Sender { return sender })

	require.NoError(t, ch.SendText("peer-1", "hi"))
	require.Len(t, client.calls, 1)
	assert.Equal(t, "hi", client.calls[0].text)

	require.NoError(t, ch.SendReply("mid", "peer-1", "again"))
	require.Len(t, client.calls, 2)
}

func TestSplitMessageShortContent(t *testing.T) {
	parts := splitMessage("短消息")
	require.Len(t, parts, 1)
	assert.Equal(t, "短消息", parts[0])
}

func TestSenderCardWithoutTitle(t *testing.T) {
	client := &fakeSenderClient{}
	sender := NewSender(client, staticTokens{})
	require.NoError(t, sender.SendCard(context.Background(), "peer-1", "  ", "正文", "blue"))
	require.Len(t, client.calls, 1)
	assert.Equal(t, "正文", client.calls[0].text)
}
