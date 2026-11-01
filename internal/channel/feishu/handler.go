package feishu

import (
	"omnibot/internal/channel/im"
	domainagent "omnibot/internal/domain/agent"
)

// 31 号 N1:飞书入站链路已上移到 internal/channel/im(渠道无关 pipeline),
// 本文件只保留飞书专属的差异配置——通道类型、任务来源、回复文案。
// MessageHandler 因此退化为 im.Handler 的类型别名,飞书原有测试继续跑同一条链路,
// 只是包内单测把方法名改为 im 的导出名(HandleMemoryCommand / HandleNoteFeedback)。

// InboundMessage 飞书入站消息(与 im.InboundMessage 同构,保留别名以兼容 channel.go)。
type InboundMessage = im.InboundMessage

// MessageHandler 飞书消息处理器(渠道无关 pipeline 的飞书实例化)。
type MessageHandler = im.Handler

// 飞书绑定相关回复文案(PRD 5.2 / 5.4)。
const (
	feishuBindSuccessReply    = "绑定成功!现在可以在飞书跟我聊了"
	feishuAlreadyBoundReply   = "该飞书号已绑定其他账号"
	feishuAccountAlreadyBound = "你的账号已绑定飞书,如需更换请稍后(暂不支持)"
)

// NewMessageHandler 创建飞书消息处理器(内部装配共享 IM pipeline)。
func NewMessageHandler(
	binding BindingService,
	msg MessageService,
	agent AgentService,
	cfg LLMConfigService,
	sender Sender,
) *im.Handler {
	return im.NewHandler(im.Options{
		ChannelType: "feishu",
		Source:      domainagent.SourceFeishu,
		Binding:     binding,
		Messages:    msg,
		Agent:       agent,
		LLMConfig:   cfg,
		Sender:      sender,

		BindSuccessReply:         feishuBindSuccessReply,
		AlreadyBoundReply:        feishuAlreadyBoundReply,
		AccountAlreadyBoundReply: feishuAccountAlreadyBound,
	})
}
