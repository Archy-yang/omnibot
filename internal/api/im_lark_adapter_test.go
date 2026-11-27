package api

// im_lark_adapter_test.go — 两个授权适配器的字段映射。
//
// 它们是 service 与 channel/工具 之间唯一的翻译层,字段漏一个的后果很具体:
//   - 漏 VerifyURL/UserCode → 用户收到一条没有链接的"请授权"
//   - 漏 ExpiresIn          → "N 分钟内有效"消失,用户不知道链接会过期
//   - 漏 UserName           → 成功提示变成"身份："空着
// 所以逐字段断言。

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	larkauthSvc "omnibot/internal/service/larkauth"
)

type fakeEnsurer struct {
	view    larkauthSvc.View
	err     error
	channel string
	target  string
	calls   int
}

func (f *fakeEnsurer) EnsureAuth(_ context.Context, _ int64, channel, target string) (larkauthSvc.View, error) {
	f.calls++
	f.channel, f.target = channel, target
	return f.view, f.err
}

// IM 适配器:把 View 全字段搬进 LarkAuthPrompt,并把 channel/target 透传下去。
func TestIMLarkAuthAdapter_MapsAllFields(t *testing.T) {
	ensurer := &fakeEnsurer{view: larkauthSvc.View{
		Status:    "pending",
		UserName:  "杨琦",
		VerifyURL: "https://verify/x?user_code=AB-1",
		UserCode:  "AB-1",
		ExpiresIn: 480,
	}}
	adapter := newIMLarkAuthAdapter(ensurer)

	prompt, err := adapter.EnsureAuth(context.Background(), 7, "feishu", "ou_peer")
	require.NoError(t, err)
	assert.Equal(t, "pending", prompt.Status)
	assert.Equal(t, "杨琦", prompt.UserName)
	assert.Equal(t, "https://verify/x?user_code=AB-1", prompt.VerifyURL)
	assert.Equal(t, "AB-1", prompt.UserCode)
	assert.Equal(t, 480, prompt.ExpiresIn, "有效期丢了用户就不知道链接会过期")
	assert.Equal(t, "feishu", ensurer.channel)
	assert.Equal(t, "ou_peer", ensurer.target)
}

func TestIMLarkAuthAdapter_PropagatesError(t *testing.T) {
	adapter := newIMLarkAuthAdapter(&fakeEnsurer{err: errors.New("boom")})
	_, err := adapter.EnsureAuth(context.Background(), 7, "feishu", "ou_peer")
	require.Error(t, err)
}

// 工具适配器:只搬工具需要的三个字段(status/url/name)。
func TestToolsLarkAuthAdapter_MapsFields(t *testing.T) {
	ensurer := &fakeEnsurer{view: larkauthSvc.View{
		Status:    "pending",
		UserName:  "杨琦",
		VerifyURL: "https://verify/y",
		UserCode:  "CD-2",
	}}
	adapter := newToolsLarkAuthAdapter(ensurer)

	prompt, err := adapter.EnsureAuthPrompt(context.Background(), 7, "wechat_claw", "peer-9")
	require.NoError(t, err)
	assert.Equal(t, "pending", prompt.Status)
	assert.Equal(t, "https://verify/y", prompt.VerifyURL, "工具结果要靠这个链接引导用户")
	assert.Equal(t, "杨琦", prompt.UserName)
	assert.Equal(t, "wechat_claw", ensurer.channel, "工具侧要按来源渠道记录,收尾才能推回同一会话")
	assert.Equal(t, "peer-9", ensurer.target)
}

func TestToolsLarkAuthAdapter_PropagatesError(t *testing.T) {
	adapter := newToolsLarkAuthAdapter(&fakeEnsurer{err: errors.New("boom")})
	_, err := adapter.EnsureAuthPrompt(context.Background(), 7, "feishu", "ou_peer")
	require.Error(t, err)
}
