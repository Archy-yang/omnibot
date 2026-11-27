package larkauth

// messages_test.go — IM 文案。
//
// 文案不是装饰:用户能不能走完授权,一半取决于这条消息说清楚了三件事
// (链接、用户码、确认后会发生什么)。所以逐条锁住关键要素。

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildAuthNeedConfig(t *testing.T) {
	text := BuildAuthNeedConfig()
	assert.Contains(t, text, "AppID")
	assert.Contains(t, text, "设置")
}

func TestBuildAuthAlreadyOK(t *testing.T) {
	assert.Contains(t, BuildAuthAlreadyOK("杨琦"), "杨琦")
	assert.Contains(t, BuildAuthAlreadyOK(""), "正常", "没有名字也要说清状态")
	assert.NotContains(t, BuildAuthAlreadyOK(""), "：", "无名时不该留个空冒号")
}

func TestBuildAuthPending(t *testing.T) {
	// 完整形态:链接 + 用户码 + 有效期 + "不用再发命令"
	full := BuildAuthPending("https://verify/x", "CODE-9", 8)
	assert.Contains(t, full, "https://verify/x")
	assert.Contains(t, full, "CODE-9")
	assert.Contains(t, full, "8 分钟")
	assert.Contains(t, full, "不用再发命令")

	// 没有用户码:省略整行,不留空标签
	noCode := BuildAuthPending("https://verify/x", "", 0)
	assert.NotContains(t, noCode, "用户码")
	assert.NotContains(t, noCode, "分钟", "有效期未知时不该编一个")
	assert.Contains(t, noCode, "https://verify/x")

	// 没拿到链接:给可执行的降级指引,不能返回空串
	empty := BuildAuthPending("", "", 0)
	assert.Contains(t, empty, "Web")
}

func TestBuildAuthSuccess(t *testing.T) {
	assert.Contains(t, BuildAuthSuccess("杨琦"), "杨琦")
	assert.Contains(t, BuildAuthSuccess("杨琦"), "已恢复")
	// 成功文案要告诉用户下一步做什么,而不是只报个状态
	assert.Contains(t, BuildAuthSuccess("杨琦"), "接着")
	assert.Contains(t, BuildAuthSuccess(""), "已恢复")
}

func TestBuildAuthReissued(t *testing.T) {
	text := BuildAuthReissued("https://verify/new")
	assert.Contains(t, text, "https://verify/new")
	assert.Contains(t, text, "过期")
	// 没有新链接时不能给一条"换了个新的："却没链接的消息
	assert.Equal(t, BuildAuthExpiredFinal(), BuildAuthReissued(""))
}

func TestBuildAuthExpiredFinalAndFailed(t *testing.T) {
	for name, text := range map[string]string{
		"二次过期": BuildAuthExpiredFinal(),
		"收尾失败": BuildAuthFailed(),
	} {
		assert.True(t, strings.Contains(text, "#飞书授权"), "%s 必须给出可执行的出路: %s", name, text)
	}
}

func TestBuildAuthProbeFailed(t *testing.T) {
	text := BuildAuthProbeFailed()
	assert.Contains(t, text, "Web")
	assert.NotContains(t, text, "授权已失效", "探测失败≠授权失效,不能吓用户")
}
