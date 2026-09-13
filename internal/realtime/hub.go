// Package realtime Web 端 WebSocket 实时推送(08 §4.8):
// 服务端→客户端长连接,替代前端轮询作为「任务完成」感知主路径(轮询降级为兜底)。
//
// 范围与边界:
//   - 推送通道 only——聊天流式仍走 SSE(POST 无状态、代理友好),WS 不承载请求方向
//   - 鉴权走首条消息(宪章 6.2 禁止 token 进 URL;浏览器 WS 无法自定义 header):
//     连接后 5s 内发 {"type":"auth","token":"<JWT>"},超时/无效即断开
//   - 心跳:服务端周期 ping,读泵收到 pong 即续命;写通道阻塞超时剔除连接
package realtime

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"go.uber.org/zap"
	"omnibot/internal/pkg/auth"

	"omnibot/pkg/logger"
)

const (
	// pingInterval 心跳周期;pongDeadline 为等待 pong 的时限(必须 > pingInterval)。
	pingInterval = 25 * time.Second
	pongDeadline = 60 * time.Second
	writeTimeout = 5 * time.Second
)

// authTimeout 连接建立后等待首条 auth 消息的时限。var 供测试收紧。
var authTimeout = 5 * time.Second

// authMessage 客户端首条消息。
type authMessage struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

// Client 一条已鉴权的 WS 连接。
type Client struct {
	conn   *websocket.Conn
	userID int64
	send   chan []byte
	done   chan struct{}
}

// Hub 连接注册表:userID → 连接集合(同一用户多标签页各持一条)。
type Hub struct {
	mu      sync.RWMutex
	clients map[int64]map[*Client]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: make(map[int64]map[*Client]struct{})}
}

func (h *Hub) add(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[c.userID] == nil {
		h.clients[c.userID] = make(map[*Client]struct{})
	}
	h.clients[c.userID][c] = struct{}{}
}

func (h *Hub) remove(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.clients[c.userID]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.clients, c.userID)
		}
	}
}

// ClientCount 该用户当前在线连接数(测试与观测用)。
func (h *Hub) ClientCount(userID int64) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[userID])
}

// Publish 向该用户的全部在线连接推送事件,返回送达数。
// 非阻塞:写通道满即丢弃该连接的本次推送(慢消费者不拖累其他连接)。
func (h *Hub) Publish(userID int64, msgType string, data interface{}) int {
	payload, err := json.Marshal(map[string]interface{}{"type": msgType, "data": data})
	if err != nil {
		logger.ErrorWithFields("realtime: marshal payload failed", zap.Error(err))
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	delivered := 0
	for c := range h.clients[userID] {
		select {
		case c.send <- payload:
			delivered++
		default:
			// 慢消费者:丢本帧,靠前端重连补漏兜底
		}
	}
	return delivered
}

// PublishTaskCompleted 实现 agent.TaskCompletionPublisher(08 §4.8):
// web 任务完成事件,前端收到后触发 /report SSE 汇报链路。
func (h *Hub) PublishTaskCompleted(userID, taskID int64) {
	h.Publish(userID, "task.completed", map[string]interface{}{"task_id": taskID})
}

// Handler gin 路由处理器:升级连接 → 等首条 auth → 注册进 Hub → 双泵运行。
func (h *Hub) Handler(jwtSvc *auth.JWTService) gin.HandlerFunc {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true }, // 同源策略由部署层 CORS 管控
	}
	return func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return // Upgrade 失败已写响应(如非 WS 请求 → 400)
		}

		// 首条消息鉴权:authTimeout 内无效/缺失即断
		_ = conn.SetReadDeadline(time.Now().Add(authTimeout))
		var msg authMessage
		if err := conn.ReadJSON(&msg); err != nil || msg.Type != "auth" {
			_ = conn.Close()
			return
		}
		userID, err := jwtSvc.ParseToken(msg.Token)
		if err != nil || userID <= 0 {
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "auth failed"), time.Now().Add(writeTimeout))
			_ = conn.Close()
			return
		}

		client := &Client{
			conn:   conn,
			userID: userID,
			send:   make(chan []byte, 16),
			done:   make(chan struct{}),
		}
		h.add(client)
		defer h.remove(client)
		logger.InfoWithFields("realtime: client connected", zap.Int64("user_id", userID))

		go h.writePump(client)
		h.readPump(client)
	}
}

// readPump 读泵:忽略一切客户端数据消息(协议里只有 auth,鉴权后无上行),
// 收到 pong 续命;连接断开时关闭 done 让写泵退出。
func (h *Hub) readPump(c *Client) {
	defer func() {
		close(c.done)
		c.conn.Close()
	}()
	_ = c.conn.SetReadDeadline(time.Now().Add(pongDeadline))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongDeadline))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// writePump 写泵:周期 ping + 转发 send 队列;done 关闭或写失败即退出。
func (h *Hub) writePump(c *Client) {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case payload, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}
