package wechat

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"omnibot/internal/client/llm"
	"omnibot/pkg/config"
	"omnibot/pkg/logger"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// MockLLMClient 用于单元测试的 Mock 客户端
type MockLLMClient struct {
	returnString string
	returnError  error
	called       bool
	lastMessages []llm.ChatMessage
}

func (m *MockLLMClient) ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	m.called = true
	m.lastMessages = messages
	return m.returnString, m.returnError
}

// MockBindingService 用于单元测试的 Mock 绑定服务(v2.3)。
// ResolveUserID 返回预设的 (userID, bound);BindChannel 返回预设 err。
type MockBindingService struct {
	resolveUserID int64
	resolveBound  bool
	resolveErr    error
	bindErr       error
	bindCalled    bool
	bindGotCode   string
	bindGotOpenID string
}

func (m *MockBindingService) BindChannel(channelType, code, openID string) error {
	m.bindCalled = true
	m.bindGotCode, m.bindGotOpenID = code, openID
	return m.bindErr
}

func (m *MockBindingService) ResolveUserID(channelType, openID string) (int64, bool, error) {
	return m.resolveUserID, m.resolveBound, m.resolveErr
}

func TestHandler_Verify_ValidSignature(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	mockLLM := &MockLLMClient{}
	mockUser := &MockBindingService{}
	handler := NewHandler(Config{
		Token: "testtoken",
	}, mockLLM, mockUser)

	r := gin.New()
	r.GET("/wechat/callback", handler.Verify)

	req := httptest.NewRequest("GET", "/wechat/callback?signature=fdf1cc36630e1abee12ce1f80ce8070e723f0fa6&timestamp=123456&nonce=abc&echostr=test_echostr", nil)
	w := httptest.NewRecorder()

	// 执行
	r.ServeHTTP(w, req)

	// 断言
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "test_echostr", w.Body.String())
	assert.False(t, mockLLM.called, "Verify 接口不应该调用 LLM")
}

func TestHandler_Verify_InvalidSignature(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	mockLLM := &MockLLMClient{}
	mockUser := &MockBindingService{}
	handler := NewHandler(Config{
		Token: "testtoken",
	}, mockLLM, mockUser)

	r := gin.New()
	r.GET("/wechat/callback", handler.Verify)

	req := httptest.NewRequest("GET", "/wechat/callback?signature=wrong&timestamp=123456&nonce=abc&echostr=test_echostr", nil)
	w := httptest.NewRecorder()

	// 执行
	r.ServeHTTP(w, req)

	// 断言
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid signature")
}

func TestHandler_HandleMessage_DoesNotRequireRawBodyLogging(t *testing.T) {
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	mockLLM := &MockLLMClient{
		returnString: "这是 LLM 生成的智能回复",
	}
	mockUser := &MockBindingService{resolveUserID: 1, resolveBound: true}
	handler := NewHandler(Config{
		Token: "testtoken",
	}, mockLLM, mockUser)

	r := gin.New()
	r.POST("/wechat/callback", handler.HandleMessage)

	xmlBody := `<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_test]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[text]]></MsgType>
  <Content><![CDATA[#记住 secret-memory-content]]></Content>
</xml>`

	req := httptest.NewRequest("POST", "/wechat/callback", bytes.NewBufferString(xmlBody))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mockLLM.called, "应该调用 LLM")
}

func TestHandler_HandleMessage_DoesNotLogRawRequestBodySource(t *testing.T) {
	source, err := os.ReadFile("handler.go")
	require.NoError(t, err)
	content := string(source)

	assert.NotContains(t, content, "zap.String(\"body\"")
	assert.NotContains(t, content, "Received raw wechat message")
	assert.Contains(t, content, "body_length")
}

func TestHandler_HandleMessage_TextMessage_LLMSuccess(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	mockLLM := &MockLLMClient{
		returnString: "这是 LLM 生成的智能回复",
	}
	mockUser := &MockBindingService{resolveUserID: 1, resolveBound: true}
	handler := NewHandler(Config{
		Token: "testtoken",
	}, mockLLM, mockUser)

	r := gin.New()
	r.POST("/wechat/callback", handler.HandleMessage)

	xmlBody := `<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_test]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[text]]></MsgType>
  <Content><![CDATA[你好]]></Content>
</xml>`

	req := httptest.NewRequest("POST", "/wechat/callback", bytes.NewBufferString(xmlBody))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	// 执行
	r.ServeHTTP(w, req)

	// 断言
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mockLLM.called, "应该调用 LLM")
	assert.Contains(t, w.Body.String(), "<![CDATA[这是 LLM 生成的智能回复]]>")
	assert.Contains(t, w.Body.String(), "<ToUserName><![CDATA[openid_test]]></ToUserName>")
	assert.Contains(t, w.Body.String(), "<FromUserName><![CDATA[gh_test]]></FromUserName>")
}

func TestHandler_HandleMessage_TextMessage_LLMFails(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	mockLLM := &MockLLMClient{
		returnError: assert.AnError,
	}
	mockUser := &MockBindingService{resolveUserID: 1, resolveBound: true}
	handler := NewHandler(Config{
		Token: "testtoken",
	}, mockLLM, mockUser)

	r := gin.New()
	r.POST("/wechat/callback", handler.HandleMessage)

	xmlBody := `<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_test]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[text]]></MsgType>
  <Content><![CDATA[你好]]></Content>
</xml>`

	req := httptest.NewRequest("POST", "/wechat/callback", bytes.NewBufferString(xmlBody))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	// 执行
	r.ServeHTTP(w, req)

	// 断言
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mockLLM.called, "应该调用 LLM")
	assert.Contains(t, w.Body.String(), "<![CDATA[服务暂时不可用，请稍后再试]]>")
}

func TestHandler_HandleMessage_ImageMessage(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	mockLLM := &MockLLMClient{
		returnString: "好的，我收到了您的图片",
	}
	mockUser := &MockBindingService{resolveUserID: 1, resolveBound: true}
	handler := NewHandler(Config{
		Token: "testtoken",
	}, mockLLM, mockUser)

	r := gin.New()
	r.POST("/wechat/callback", handler.HandleMessage)

	xmlBody := `<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_test]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[image]]></MsgType>
  <PicUrl><![CDATA[http://example.com/pic.jpg]]></PicUrl>
  <MediaId><![CDATA[media_id]]></MediaId>
</xml>`

	req := httptest.NewRequest("POST", "/wechat/callback", bytes.NewBufferString(xmlBody))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	// 执行
	r.ServeHTTP(w, req)

	// 断言
	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mockLLM.called, "应该调用 LLM")
	assert.Equal(t, 2, len(mockLLM.lastMessages), "应该包含 system 和 user 两条消息")
	assert.Equal(t, "system", mockLLM.lastMessages[0].Role)
	assert.Equal(t, "你是一个友好的智能客服助手，请用简洁的中文回应用户的问题。", mockLLM.lastMessages[0].Content)
	assert.Equal(t, "user", mockLLM.lastMessages[1].Role)
	assert.Equal(t, "用户发送了一张图片", mockLLM.lastMessages[1].Content)
}

func TestHandler_HandleMessage_SubscribeEvent_CreatesUser(t *testing.T) {
	// v2.3: 关注事件不再建号/调 LLM,回固定欢迎语 + 绑定引导
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	mockLLM := &MockLLMClient{
		returnString: "欢迎关注我们的公众号！",
	}
	mockUser := &MockBindingService{resolveUserID: 1, resolveBound: true}
	handler := NewHandler(Config{
		Token: "testtoken",
	}, mockLLM, mockUser)

	r := gin.New()
	r.POST("/wechat/callback", handler.HandleMessage)

	xmlBody := `<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_test]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[event]]></MsgType>
  <Event><![CDATA[subscribe]]></Event>
</xml>`

	req := httptest.NewRequest("POST", "/wechat/callback", bytes.NewBufferString(xmlBody))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	// 执行
	r.ServeHTTP(w, req)

	// 断言:回欢迎语 + 绑定引导,不调 LLM
	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, mockLLM.called, "v2.3 关注事件不调 LLM,回固定引导文案")
	assert.Contains(t, w.Body.String(), "欢迎关注 OmniBot")
	assert.Contains(t, w.Body.String(), "绑定")
}

func TestHandler_HandleMessage_SubscribeEvent_UserServiceError(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	mockLLM := &MockLLMClient{
		returnString: "欢迎关注我们的公众号！",
	}
	mockUser := &MockBindingService{resolveErr: assert.AnError}
	handler := NewHandler(Config{
		Token: "testtoken",
	}, mockLLM, mockUser)

	r := gin.New()
	r.POST("/wechat/callback", handler.HandleMessage)

	xmlBody := `<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_test]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[event]]></MsgType>
  <Event><![CDATA[subscribe]]></Event>
</xml>`

	req := httptest.NewRequest("POST", "/wechat/callback", bytes.NewBufferString(xmlBody))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	// 执行
	r.ServeHTTP(w, req)

	// 断言:v2.3 订阅事件不依赖 bindingService,直接回固定引导文案
	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, mockLLM.called, "v2.3 关注事件不调 LLM")
	assert.Contains(t, w.Body.String(), "欢迎关注 OmniBot")
}

func TestHandler_HandleMessage_Unsubscribe_NoResponse(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	mockLLM := &MockLLMClient{}
	mockUser := &MockBindingService{}
	handler := NewHandler(Config{
		Token: "testtoken",
	}, mockLLM, mockUser)

	r := gin.New()
	r.POST("/wechat/callback", handler.HandleMessage)

	xmlBody := `<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_test]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[event]]></MsgType>
  <Event><![CDATA[unsubscribe]]></Event>
</xml>`

	req := httptest.NewRequest("POST", "/wechat/callback", bytes.NewBufferString(xmlBody))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	// 执行
	r.ServeHTTP(w, req)

	// 断言
	assert.Equal(t, http.StatusOK, w.Code)
	assert.False(t, mockLLM.called, "取消订阅事件不应该调用 LLM")
	assert.Equal(t, "", w.Body.String(), "取消订阅事件不回复消息")
}
