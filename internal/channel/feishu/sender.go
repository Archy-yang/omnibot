package feishu

import (
	"context"
	"encoding/json"
	"fmt"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// larkSender 用飞书官方 SDK 发文本消息。
//
// 飞书文本消息 content 必须是 JSON 字符串 `{"text":"..."}`,且 receive_id_type=open_id 时
// ReceiveId 必须传 open_id。其他字段(uuid/msg_card/post 等)v1.6 不用,留待后续富消息扩展。
//
// 此类型不参与单元测试--它是 SDK adapter,通过 Sender 接口在 handler 层 mock。端到端
// 行为靠真实飞书 smoke 验证(测试计划 v1.6 第 5-9 步)。
type larkSender struct {
	client *lark.Client
}

// NewLarkSender 创建基于飞书官方 SDK 的发送器。
func NewLarkSender(client *lark.Client) Sender {
	return &larkSender{client: client}
}

// SendText 通过飞书 API 给指定 open_id 发**纯文本**消息。
// 飞书客户端不渲染 markdown,**`**bold**` 这类语法会原样显示**。
// 适合 fallback 兜底等短文本场景;Agent 输出请用 SendMarkdown。
//
// 失败包含两种:HTTP/网络错误(直接 err)和 API 业务错误(resp.Code != 0)。
// 两种都转为 error 返回,由调用方(handler)决定是否记录或上抛。
func (s *larkSender) SendText(ctx context.Context, openID, content string) error {
	textContent, err := json.Marshal(map[string]string{"text": content})
	if err != nil {
		return fmt.Errorf("feishu: marshal text content: %w", err)
	}
	return s.createMessage(ctx, openID, "text", string(textContent))
}

// SendMarkdown 通过飞书 API 给指定 open_id 发**支持 markdown 渲染**的消息。
//
// 实现:用 MsgType="interactive" 卡片(JSON 2.0),卡片只有一个 markdown element 包裹原文。
// 这是飞书 IM **唯一**渲染 markdown 的姿势--没有 MsgType="markdown" 这种东西
// (网上很多老博客有此错误信息),`post` 类型要求把 markdown 解析成飞书私有结构,
// 等于重新实现一个 markdown parser,又重又脆。
//
// 飞书 markdown element 支持子集:**bold** *italic* ~~strike~~ [link](url)
// 多级标题 列表 引用 行内代码 “ ``` 围栏代码块 emoji。不支持表格、HTML 标签。
//
// 卡片用 JSON 2.0 结构(schema:"2.0" + body.elements),无 header(纯正文)。
// 带 header 标题的卡片用 SendCard。
func (s *larkSender) SendMarkdown(ctx context.Context, openID, content string) error {
	cardJSON, err := json.Marshal(buildMarkdownCard(content))
	if err != nil {
		return fmt.Errorf("feishu: marshal markdown card: %w", err)
	}
	return s.createMessage(ctx, openID, "interactive", string(cardJSON))
}

// SendCard 通过飞书 API 发**带 header 标题**的 JSON 2.0 卡片。
// 供回执推送等需要标题的场景;主对话回复用 SendMarkdown(无 header)。
//
// title:卡片标题;content:markdown 正文;template:标题主题色(blue/green/orange/red...),空=默认。
func (s *larkSender) SendCard(ctx context.Context, openID, title, content, template string) error {
	cardJSON, err := json.Marshal(buildCard(title, content, template))
	if err != nil {
		return fmt.Errorf("feishu: marshal card: %w", err)
	}
	return s.createMessage(ctx, openID, "interactive", string(cardJSON))
}

// buildMarkdownCard 构造无 header 的 JSON 2.0 卡片(纯 markdown 正文)。
// 抽纯函数便于单测结构(schema 2.0 + body.elements,非 1.0 顶层 elements)。
func buildMarkdownCard(content string) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"body": map[string]any{
			"elements": []map[string]any{
				{"tag": "markdown", "content": content},
			},
		},
	}
}

// buildCard 构造带 header 标题的 JSON 2.0 卡片。
// title:标题;content:markdown 正文;template:标题色(空则不设,飞书默认 default)。
func buildCard(title, content, template string) map[string]any {
	header := map[string]any{
		"title": map[string]any{"tag": "plain_text", "content": title},
	}
	if template != "" {
		header["template"] = template
	}
	return map[string]any{
		"schema": "2.0",
		"header": header,
		"body": map[string]any{
			"elements": []map[string]any{
				{"tag": "markdown", "content": content},
			},
		},
	}
}

// createMessage 公共调用路径,把 (msgType, content JSON 字符串) 发出去。
func (s *larkSender) createMessage(ctx context.Context, openID, msgType, content string) error {
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("open_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(openID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build()

	resp, err := s.client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("feishu: create message: %w", err)
	}
	if resp != nil && resp.Code != 0 {
		return fmt.Errorf("feishu: create message api error: %s", resp.Error())
	}
	return nil
}
