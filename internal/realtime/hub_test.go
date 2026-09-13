package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"omnibot/internal/pkg/auth"
)

// realtime Hub 测试(08 §4.8):
// 首条消息鉴权成功/失败/超时、按用户推送、同用户多连接广播、Publish 返回送达数。

func newTestServer(t *testing.T, hub *Hub, secret string) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	jwtSvc := auth.NewJWTService(secret, time.Hour)
	r.GET("/ws", hub.Handler(jwtSvc))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + "/ws"
}

func dialAndAuth(t *testing.T, rawURL, token string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(rawURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.WriteJSON(authMessage{Type: "auth", Token: token}); err != nil {
		t.Fatalf("auth write: %v", err)
	}
	return conn
}

// waitEvent 读一条服务端消息(跳过可能的中间帧),解析为通用 map。
func waitEvent(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read event: %v", err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(payload, &m); err != nil {
			t.Fatalf("bad json: %v", err)
		}
		if m["type"] == "ping" {
			_ = conn.WriteMessage(websocket.PongMessage, nil)
			continue
		}
		return m
	}
}

func TestHub_AuthAndPush_SingleClient(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(t, hub, "test-secret")
	jwtSvc := auth.NewJWTService("test-secret", time.Hour)
	token, err := jwtSvc.GenerateToken(42)
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	conn := dialAndAuth(t, wsURL(srv.URL), token)
	// 注册是异步的:等一小会
	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount(42) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount(42) == 0 {
		t.Fatal("client not registered after auth")
	}

	if n := hub.Publish(42, "task.completed", map[string]interface{}{"task_id": 7}); n != 1 {
		t.Fatalf("expected 1 delivered, got %d", n)
	}

	m := waitEvent(t, conn)
	if m["type"] != "task.completed" {
		t.Fatalf("unexpected event: %v", m)
	}
	data, ok := m["data"].(map[string]interface{})
	if !ok || data["task_id"].(float64) != 7 {
		t.Fatalf("unexpected payload: %v", m)
	}
}

func TestHub_AuthReject_InvalidToken(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(t, hub, "test-secret")

	conn := dialAndAuth(t, wsURL(srv.URL), "not-a-jwt")
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("expected connection close on invalid token")
	}
	if hub.ClientCount(42) != 0 {
		t.Fatal("invalid-token client must not be registered")
	}
}

func TestHub_AuthTimeout(t *testing.T) {
	orig := authTimeout
	authTimeout = 300 * time.Millisecond
	t.Cleanup(func() { authTimeout = orig })

	hub := NewHub()
	srv := newTestServer(t, hub, "test-secret")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL(srv.URL), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// 不发 auth:服务端应在 authTimeout 内断开(远小于客户端 5s deadline)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Fatal("expected connection close on auth timeout")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("server should close at authTimeout(300ms), took %v", elapsed)
	}
}

func TestHub_MultipleConnections_SameUser(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(t, hub, "test-secret")
	jwtSvc := auth.NewJWTService("test-secret", time.Hour)
	token, _ := jwtSvc.GenerateToken(7)

	conn1 := dialAndAuth(t, wsURL(srv.URL), token)
	conn2 := dialAndAuth(t, wsURL(srv.URL), token)

	deadline := time.Now().Add(2 * time.Second)
	for hub.ClientCount(7) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount(7) != 2 {
		t.Fatalf("expected 2 clients, got %d", hub.ClientCount(7))
	}

	if n := hub.Publish(7, "task.completed", map[string]interface{}{"task_id": 1}); n != 2 {
		t.Fatalf("expected 2 delivered, got %d", n)
	}
	// 两个连接都收到
	waitEvent(t, conn1)
	waitEvent(t, conn2)

	// 其他用户收不到
	if n := hub.Publish(99, "task.completed", map[string]interface{}{}); n != 0 {
		t.Fatalf("expected 0 delivered for offline user, got %d", n)
	}
}

func TestHub_Handler_RejectsNonUpgrade(t *testing.T) {
	hub := NewHub()
	srv := newTestServer(t, hub, "test-secret")
	resp, err := http.Get(srv.URL + "/ws")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-upgrade request, got %d", resp.StatusCode)
	}
}
