package larkauth

// messages.go — IM 内授权闭环的全部文案(v2.13)。
// 集中在一处:命令回复与后台推送共用同一套措辞,改文案不用翻两遍代码。

import "fmt"

// BuildAuthNeedConfig 从未配置过飞书应用(没有 AppID/Secret):只能在 Web 完成首次配置。
func BuildAuthNeedConfig() string {
	return "还没有配置过飞书应用。请先在 Web「设置 → 飞书授权」里填写 AppID 和 App Secret。"
}

// BuildAuthAlreadyOK 当前授权可用,不需要重新授权。
func BuildAuthAlreadyOK(userName string) string {
	if userName == "" {
		return "飞书授权正常，不需要重新授权。"
	}
	return fmt.Sprintf("飞书授权正常，当前身份：%s，不需要重新授权。", userName)
}

// BuildAuthPending 把验证链接(和用户码)交给用户去浏览器确认。
// minutes<=0 时省略有效期提示。
func BuildAuthPending(verifyURL, userCode string, minutes int) string {
	if verifyURL == "" {
		return "已发起飞书授权，但没拿到验证链接。请稍后在 Web 设置里重试。"
	}
	body := fmt.Sprintf("飞书授权还差你确认这一步：\n%s", verifyURL)
	if userCode != "" {
		body += fmt.Sprintf("\n\n用户码：%s", userCode)
	}
	switch {
	case minutes > 0:
		body += fmt.Sprintf("\n\n%d 分钟内有效。确认后我会自动完成，不用再发命令。", minutes)
	default:
		body += "\n\n确认后我会自动完成，不用再发命令。"
	}
	return body
}

// BuildAuthSuccess 收尾成功后的主动推送。
func BuildAuthSuccess(userName string) string {
	if userName == "" {
		return "✅ 飞书授权已恢复。刚才没做完的事，说一声我接着办。"
	}
	return fmt.Sprintf("✅ 飞书授权已恢复（身份：%s）。刚才没做完的事，说一声我接着办。", userName)
}

// BuildAuthReissued 链接过期后自动换发的推送。
func BuildAuthReissued(verifyURL string) string {
	if verifyURL == "" {
		return BuildAuthExpiredFinal()
	}
	return fmt.Sprintf("⏳ 上一个授权链接已过期，我换了个新的：\n%s\n\n确认后我会自动完成，不用再发命令。", verifyURL)
}

// BuildAuthExpiredFinal 连续过期、已停止自动换发。
func BuildAuthExpiredFinal() string {
	return "⌛ 授权链接又过期了。回复 #飞书授权 我再给你一个新的。"
}

// BuildAuthFailed 收尾失败(链接失效/被拒绝/CLI 报错)。
func BuildAuthFailed() string {
	return "⚠️ 飞书授权没有完成（链接失效或被拒绝）。回复 #飞书授权 重试。"
}

// BuildAuthProbeFailed 探测授权状态失败(环境问题,不是授权失效)。
func BuildAuthProbeFailed() string {
	return "暂时检查不了飞书授权状态，请稍后在 Web 设置里重试。"
}
