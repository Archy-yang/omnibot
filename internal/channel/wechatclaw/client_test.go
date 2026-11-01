package wechatclaw

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorded 记录一次被调用到的请求,供断言使用。
type recorded struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
}

// newTestClient 起 httptest server 并把 client 指过去,返回记录下来的请求。
func newTestClient(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) (*Client, *[]recorded) {
	t.Helper()
	var recs []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		recs = append(recs, recorded{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.Query(),
			Header: r.Header.Clone(),
			Body:   body,
		})
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	return NewClient(ClientOptions{
		BaseURL:       srv.URL,
		Token:         "tok-123",
		ClientVersion: 132106,
		UINGenerator:  func() string { return "12345" },
	}), &recs
}

func TestClientCommonHeaders(t *testing.T) {
	client, recs := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ret":0,"msgs":[],"get_updates_buf":"buf2"}`))
	})

	_, err := client.GetUpdates(context.Background(), "buf1")
	require.NoError(t, err)
	require.Len(t, *recs, 1)

	req := (*recs)[0]
	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "/ilink/bot/getupdates", req.Path)
	assert.Equal(t, "bot", req.Header.Get("iLink-App-Id"))
	assert.Equal(t, "132106", req.Header.Get("iLink-App-ClientVersion"))
	assert.Equal(t, "ilink_bot_token", req.Header.Get("AuthorizationType"))
	assert.Equal(t, "Bearer tok-123", req.Header.Get("Authorization"))
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))
	// X-WECHAT-UIN 是十进制字符串再 base64
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte("12345")), req.Header.Get("X-WECHAT-UIN"))
}

func TestGetUpdatesRequestAndParse(t *testing.T) {
	client, recs := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// message_id 在协议里是 uint64 数字,必须能读成字符串(不能用 float64 丢精度)
		_, _ = w.Write([]byte(`{
			"ret":0,
			"msgs":[{
				"message_id": 7371234567890123456,
				"from_user_id":"u1","to_user_id":"bot1","context_token":"ctx-1",
				"message_type":1,"message_state":2,
				"item_list":[{"type":1,"text_item":{"text":"你好"}}]
			}],
			"get_updates_buf":"buf2",
			"longpolling_timeout_ms":30000
		}`))
	})

	resp, err := client.GetUpdates(context.Background(), "buf1")
	require.NoError(t, err)

	require.Len(t, resp.Msgs, 1)
	assert.Equal(t, FlexString("7371234567890123456"), resp.Msgs[0].MessageID)
	assert.Equal(t, "ctx-1", resp.Msgs[0].ContextToken)
	require.Len(t, resp.Msgs[0].ItemList, 1)
	assert.Equal(t, "你好", resp.Msgs[0].ItemList[0].TextItem.Text)
	assert.Equal(t, "buf2", resp.GetUpdatesBuf)
	assert.Equal(t, 30000, resp.LongPollingTimeoutMS)

	// 请求体带游标 + base_info
	var reqBody map[string]interface{}
	require.NoError(t, json.Unmarshal((*recs)[0].Body, &reqBody))
	assert.Equal(t, "buf1", reqBody["get_updates_buf"])
	baseInfo, ok := reqBody["base_info"].(map[string]interface{})
	require.True(t, ok)
	assert.NotEmpty(t, baseInfo["channel_version"])
}

func TestGetUpdatesSessionExpired(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ret":-14,"errcode":-14,"errmsg":"session timeout"}`))
	})

	_, err := client.GetUpdates(context.Background(), "")
	assert.ErrorIs(t, err, ErrSessionExpired)
}

func TestSendTextPayload(t *testing.T) {
	client, recs := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message_id": 1234567890123456789, "ret":0}`))
	})

	msgID, err := client.SendText(context.Background(), "u1", "回复内容", "ctx-1", "cid-1")
	require.NoError(t, err)
	assert.Equal(t, "1234567890123456789", msgID)

	assert.Equal(t, "/ilink/bot/sendmessage", (*recs)[0].Path)

	var reqBody map[string]interface{}
	require.NoError(t, json.Unmarshal((*recs)[0].Body, &reqBody))
	msg, ok := reqBody["msg"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "u1", msg["to_user_id"])
	assert.Equal(t, "ctx-1", msg["context_token"])
	assert.Equal(t, "cid-1", msg["client_id"])
	assert.Equal(t, float64(2), msg["message_type"])  // BOT
	assert.Equal(t, float64(2), msg["message_state"]) // FINISH
	items, ok := msg["item_list"].([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	item := items[0].(map[string]interface{})
	assert.Equal(t, float64(1), item["type"])
	assert.Equal(t, "回复内容", item["text_item"].(map[string]interface{})["text"])
}

func TestSendTextServerError(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ret":-1,"errmsg":"boom"}`))
	})
	_, err := client.SendText(context.Background(), "u1", "hi", "", "cid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestGetBotQRCode(t *testing.T) {
	client, recs := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"qrcode":"qc-1","qrcode_img_content":"https://liteapp.weixin.qq.com/q/x","ret":0}`))
	})

	qr, err := client.GetBotQRCode(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "qc-1", qr.QRCode)
	assert.Equal(t, "https://liteapp.weixin.qq.com/q/x", qr.ImgContent)

	req := (*recs)[0]
	assert.Equal(t, http.MethodGet, req.Method)
	assert.Equal(t, "/ilink/bot/get_bot_qrcode", req.Path)
	assert.Equal(t, "3", req.Query.Get("bot_type"))
}

func TestGetQRCodeStatus(t *testing.T) {
	client, recs := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"confirmed","bot_token":"bt-1","ilink_bot_id":"bot-1","ilink_user_id":"u-9","baseurl":"https://ilinkai.weixin.qq.com","ret":0}`))
	})

	st, err := client.GetQRCodeStatus(context.Background(), "qc-1", "8888")
	require.NoError(t, err)
	assert.Equal(t, "confirmed", st.Status)
	assert.Equal(t, "bt-1", st.BotToken)
	assert.Equal(t, "bot-1", st.BotID)
	assert.Equal(t, "u-9", st.UserID)
	assert.Equal(t, "https://ilinkai.weixin.qq.com", st.BaseURL)

	req := (*recs)[0]
	assert.Equal(t, "/ilink/bot/get_qrcode_status", req.Path)
	assert.Equal(t, "qc-1", req.Query.Get("qrcode"))
	assert.Equal(t, "8888", req.Query.Get("verify_code"))
}

func TestNotifyStartStop(t *testing.T) {
	client, recs := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ret":0}`))
	})

	require.NoError(t, client.NotifyStart(context.Background()))
	require.NoError(t, client.NotifyStop(context.Background()))

	require.Len(t, *recs, 2)
	assert.Equal(t, "/ilink/bot/msg/notifystart", (*recs)[0].Path)
	assert.Equal(t, "/ilink/bot/msg/notifystop", (*recs)[1].Path)
}

func TestHTTPErrorPropagates(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`bad gateway`))
	})
	_, err := client.GetUpdates(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

func TestGetUpdatesClientTimeoutReturnsEmpty(t *testing.T) {
	// 服务端挂住超过长轮询超时:客户端应视为正常控制流,返回空结果 + 原游标
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		_, _ = w.Write([]byte(`{"ret":0,"msgs":[],"get_updates_buf":"late"}`))
	})
	client.longPollout = 20 * time.Millisecond

	resp, err := client.GetUpdates(context.Background(), "buf-1")
	require.NoError(t, err)
	assert.Empty(t, resp.Msgs)
	assert.Equal(t, "buf-1", resp.GetUpdatesBuf, "超时应保留原游标")
}

func TestGetBotQRCodeServerRetError(t *testing.T) {
	client, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ret":-2,"errmsg":"quota"}`))
	})
	_, err := client.GetBotQRCode(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota")
}

func TestClientAccessorsAndDefaults(t *testing.T) {
	c := NewClient(ClientOptions{BaseURL: "https://x.test/", Token: "t"})
	assert.Equal(t, "https://x.test", c.BaseURL())
	assert.Equal(t, "t", c.Token())

	// 默认值兜底
	def := NewClient(ClientOptions{})
	assert.Equal(t, DefaultBaseURL, def.BaseURL())
	assert.Equal(t, DefaultClientVersion, def.clientVersion)
	assert.NotEmpty(t, randomUIN())
}

func TestFlexStringUnmarshalVariants(t *testing.T) {
	cases := []struct {
		in   string
		want FlexString
	}{
		{`"123"`, FlexString("123")},
		{`123`, FlexString("123")},
		{`7371234567890123456`, FlexString("7371234567890123456")},
		{`null`, FlexString("")},
		{`""`, FlexString("")},
	}
	for _, tc := range cases {
		var f FlexString
		require.NoError(t, json.Unmarshal([]byte(tc.in), &f), tc.in)
		assert.Equal(t, tc.want, f, tc.in)
	}
}

func TestMessageTextExtraction(t *testing.T) {
	m := Message{ItemList: []MessageItem{
		{Type: ItemTypeImage},
		{Type: ItemTypeText, TextItem: &TextItem{Text: "hello"}},
	}}
	assert.Equal(t, "hello", m.Text())

	empty := Message{ItemList: []MessageItem{{Type: ItemTypeImage}}}
	assert.Empty(t, empty.Text())
}
