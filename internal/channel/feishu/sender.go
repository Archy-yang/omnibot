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
// 此类型不参与单元测试——它是 SDK adapter,通过 Sender 接口在 handler 层 mock。端到端
// 行为靠真实飞书 smoke 验证(测试计划 v1.6 第 5-9 步)。
type larkSender struct {
	client *lark.Client
}

// NewLarkSender 创建基于飞书官方 SDK 的发送器。
func NewLarkSender(client *lark.Client) Sender {
	return &larkSender{client: client}
}

// SendText 通过飞书 API 给指定 open_id 发文本消息。
// 失败包含两种:HTTP/网络错误(直接 err)和 API 业务错误(resp.Code != 0)。
// 两种都转为 error 返回,由调用方(handler)决定是否记录或上抛。
func (s *larkSender) SendText(ctx context.Context, openID, content string) error {
	textContent, err := json.Marshal(map[string]string{"text": content})
	if err != nil {
		return fmt.Errorf("feishu: marshal text content: %w", err)
	}

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("open_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(openID).
			MsgType("text").
			Content(string(textContent)).
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
