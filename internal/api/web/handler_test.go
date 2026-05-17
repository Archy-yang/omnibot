package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"omnibot/internal/client/llm"
	domainuser "omnibot/internal/domain/user"
)

type mockUserService struct {
	userID       int64
	channelID    string
	created      bool
}

func (m *mockUserService) GetOrCreateByChannel(channelType, channelUserID string) (*domainuser.User, *domainuser.UserChannel, bool, error) {
	m.channelID = channelUserID
	return &domainuser.User{ID: m.userID}, nil, m.created, nil
}

type mockMessageService struct {
	savedUserContent     string
	savedAssistantContent string
}

func (m *mockMessageService) SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) error {
	m.savedUserContent = content
	return nil
}

func (m *mockMessageService) SaveAssistantMessage(ctx context.Context, userID int64, content string) error {
	m.savedAssistantContent = content
	return nil
}

func (m *mockMessageService) BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error) {
	return []llm.ChatMessage{
		{Role: "user", Content: currentContent},
	}, nil
}

type mockLLMClient struct {
	calledWithMessages []llm.ChatMessage
}

func (m *mockLLMClient) ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	m.calledWithMessages = messages
	return "AI response", nil
}

func TestHandleSendMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup
	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	llmClient := &mockLLMClient{}

	handler := NewHandler(userSvc, msgSvc, llmClient)

	router := gin.Default()
	router.POST("/api/v1/chat/messages", handler.HandleSendMessage)

	// Test request
	reqBody := map[string]string{
		"session_id": "test-session-123",
		"content":    "Hello OmniBot",
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/v1/chat/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "AI response")
	assert.Equal(t, "test-session-123", userSvc.channelID)
	assert.Equal(t, "Hello OmniBot", msgSvc.savedUserContent)
	assert.Equal(t, "AI response", msgSvc.savedAssistantContent)
}

func TestHandleGetHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup with mock messages
	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	llmClient := &mockLLMClient{}

	handler := NewHandler(userSvc, msgSvc, llmClient)

	// Test request
	router := gin.Default()
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)

	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?session_id=test-session-123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "messages")
}
