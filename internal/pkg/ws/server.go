package ws

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	sendQueueSize = 256
	writeDeadline = 10 * time.Second
	pingInterval  = 30 * time.Second
	pongTimeout   = 240 * time.Second // 4 分钟内任意一次 Pong 算活
)

var (
	errClosedConn    = errors.New("ws conn closed")
	errSendQueueFull = errors.New("ws send queue full")
)

var _ app.WSConn = (*Conn)(nil)

// upgrader 用于 master 的 agent WS 端点 /ws/agent。
// CheckOrigin 直接放行——该端点只接 agent 客户端（机器对机器，要先 enroll
// 拿到 X-Vaala-Agent-ID/Secret header 才能通过 hub.HandleWS 的鉴权），
// 浏览器既无法注入这两个自定义 header，CORS preflight 也拦不住 WS handshake，
// 但缺 header 会被 hub 立即 401。故 Origin 白名单不构成实际防御层。
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Conn struct {
	WS     *websocket.Conn
	Logger *zap.Logger

	sendQueue chan []byte
	sendDone  chan struct{}
	closed    chan struct{}
	once      sync.Once
}

func NewConn(ws *websocket.Conn, logger *zap.Logger) *Conn {
	c := &Conn{
		WS:        ws,
		Logger:    logger,
		sendQueue: make(chan []byte, sendQueueSize),
		sendDone:  make(chan struct{}),
		closed:    make(chan struct{}),
	}
	// 应用层 Ping/Pong：240s 内任意一次 Pong 算活；连续 240s 没 Pong 才判死
	ws.SetReadDeadline(time.Now().Add(pongTimeout))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(pongTimeout))
	})
	go c.writeLoop()
	return c
}

// writeLoop 是 Conn 的唯一写出 goroutine。
// 串行化所有 WriteJSON 调用（消除原 mutex），
// 同时维护应用层 Ping/Pong（30s 主动 Ping，10s 写超时）。
func (c *Conn) writeLoop() {
	defer close(c.sendDone)
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-c.sendQueue:
			if !ok {
				return
			}
			c.WS.SetWriteDeadline(time.Now().Add(writeDeadline))
			if err := c.WS.WriteMessage(websocket.TextMessage, msg); err != nil {
				if c.Logger != nil {
					c.Logger.Warn("ws write failed, closing conn", zap.Error(err))
				}
				c.WS.Close()
				return
			}
		case <-ticker.C:
			c.WS.SetWriteDeadline(time.Now().Add(writeDeadline))
			if err := c.WS.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeDeadline)); err != nil {
				if c.Logger != nil {
					c.Logger.Warn("ws ping failed, closing conn", zap.Error(err))
				}
				c.WS.Close()
				return
			}
		case <-c.closed:
			return
		}
	}
}

func (c *Conn) SendNotification(method string, params any) error {
	msg, err := jsonrpc.NewNotification(method, params)
	if err != nil {
		return err
	}
	return c.WriteJSON(msg)
}

func (c *Conn) SendResponse(resp *jsonrpc.Response) error {
	return c.WriteJSON(resp)
}

func (c *Conn) ReadMessage() (*jsonrpc.Request, *jsonrpc.Response, error) {
	_, data, err := c.WS.ReadMessage()
	if err != nil {
		return nil, nil, err
	}

	var req jsonrpc.Request
	if err := json.Unmarshal(data, &req); err == nil && req.Method != "" {
		return &req, nil, nil
	}

	var resp jsonrpc.Response
	if err := json.Unmarshal(data, &resp); err == nil && resp.ID != nil {
		return nil, &resp, nil
	}

	return nil, nil, nil
}

// WriteJSON marshal v 后非阻塞 enqueue 到 sendQueue。
// 队列满 = 本 conn 已经病了 → 主动 Close 触发对端重连（agent 重连会触发 full sync）。
// 一般调用方（如 Broadcast fan-out）忽略错误。
func (c *Conn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	select {
	case c.sendQueue <- data:
		return nil
	case <-c.closed:
		return errClosedConn
	default:
		if c.Logger != nil {
			c.Logger.Warn("ws send queue full, closing conn",
				zap.Int("queue_cap", cap(c.sendQueue)))
		}
		c.Close()
		return errSendQueueFull
	}
}

// Close 标记 closed → 等 writeLoop 退出 → 关 underlying WS。
// 不直接 close(sendQueue)，因为其他 goroutine 可能并发 enqueue，会 panic。
func (c *Conn) Close() error {
	c.once.Do(func() {
		close(c.closed)
	})
	err := c.WS.Close()
	<-c.sendDone
	return err
}

func (c *Conn) Done() <-chan struct{} {
	return c.closed
}

// Upgrade 升级 HTTP 连接到 WebSocket。
func Upgrade(w http.ResponseWriter, r *http.Request, logger *zap.Logger) (*Conn, error) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}
	return NewConn(ws, logger), nil
}
