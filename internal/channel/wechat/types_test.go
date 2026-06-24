package wechat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_TextMessage(t *testing.T) {
	body := []byte(`<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_user]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[text]]></MsgType>
  <Content><![CDATA[你好]]></Content>
  <MsgId>987654</MsgId>
</xml>`)

	in, err := Parse(body)

	require.NoError(t, err)
	require.NotNil(t, in)
	assert.Equal(t, "text", in.MsgType)
	assert.Equal(t, "你好", in.Content)
	assert.Equal(t, "openid_user", in.FromUserName)
	assert.Equal(t, "gh_test", in.ToUserName)
	assert.Equal(t, "987654", in.MsgID)
}

func TestParse_ImageMessage(t *testing.T) {
	body := []byte(`<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_user]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[image]]></MsgType>
  <PicUrl><![CDATA[http://example.com/p.jpg]]></PicUrl>
  <MediaId><![CDATA[media_123]]></MediaId>
</xml>`)

	in, err := Parse(body)

	require.NoError(t, err)
	assert.Equal(t, "image", in.MsgType)
	assert.Equal(t, "http://example.com/p.jpg", in.PicURL)
	assert.Equal(t, "media_123", in.MediaID)
}

func TestParse_SubscribeEvent(t *testing.T) {
	body := []byte(`<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_user]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[event]]></MsgType>
  <Event><![CDATA[subscribe]]></Event>
</xml>`)

	in, err := Parse(body)

	require.NoError(t, err)
	assert.Equal(t, "event", in.MsgType)
	assert.Equal(t, "subscribe", in.Event)
}

func TestParse_InvalidXML(t *testing.T) {
	body := []byte(`not xml at all`)

	in, err := Parse(body)

	assert.Error(t, err)
	assert.Nil(t, in)
}

func TestParse_EmptyBody(t *testing.T) {
	in, err := Parse(nil)

	assert.Error(t, err)
	assert.Nil(t, in)
}
