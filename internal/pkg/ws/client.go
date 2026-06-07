package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

var _ app.WSClient = (*Client)(nil)

// defaultCallTimeout 是 Call 的兜底超时(连接存活但对端不回包时生效)。
const defaultCallTimeout = 30 * time.Second

type Client struct {
	Conn     *Conn
	pending  utils.SyncMap[int64, chan *jsonrpc.Response]
	nextID   atomic.Int64
	handlers map[string]app.NotificationHandler
	mu       sync.RWMutex
	logger   *zap.Logger
	// CallTimeout 是单次 Call 的默认超时;<=0 关闭。装配代码可覆盖。
	CallTimeout time.Duration
}

// Dial 用默认 dialer 拨号（行为与历史一致）。
func Dial(ctx context.Context, url string, logger *zap.Logger, headers ...http.Header) (*Client, error) {
	dialer := &websocket.Dialer{
		HandshakeTimeout: 45 * time.Second,
		NetDialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	return DialWithDialer(ctx, dialer, url, logger, headers...)
}

// DialWithDialer 用调用方提供的 dialer 拨号，便于注入 unix NetDialContext。
func DialWithDialer(ctx context.Context, dialer *websocket.Dialer, url string, logger *zap.Logger, headers ...http.Header) (*Client, error) {
	var h http.Header
	if len(headers) > 0 {
		h = headers[0]
	}
	ws, _, err := dialer.DialContext(ctx, url, h)
	if err != nil {
		return nil, err
	}
	c := &Client{
		Conn:        NewConn(ws, logger),
		handlers:    make(map[string]app.NotificationHandler),
		logger:      logger,
		CallTimeout: defaultCallTimeout,
	}
	go c.readLoop()
	return c, nil
}

func NewClientFromConn(conn *Conn) *Client {
	return &Client{
		Conn:        conn,
		handlers:    make(map[string]app.NotificationHandler),
		logger:      conn.Logger,
		CallTimeout: defaultCallTimeout,
	}
}

func (c *Client) OnNotification(method string, handler app.NotificationHandler) {
	c.mu.Lock()
	c.handlers[method] = handler
	c.mu.Unlock()
}

func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.CallTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.CallTimeout)
		defer cancel()
	}

	// 连接已死:直接归一化为 ErrConnClosed,避免返回五花八门的底层写错误
	// (上层 classifyResolveErr 才能判成 master_unreachable),也省去无谓的
	// pending 注册 / WriteJSON。
	select {
	case <-c.Conn.Done():
		return nil, ErrConnClosed
	default:
	}

	id := c.nextID.Add(1)
	req, err := jsonrpc.NewRequest(method, params, &id)
	if err != nil {
		return nil, err
	}

	ch := make(chan *jsonrpc.Response, 1)
	c.pending.Store(id, ch)
	defer c.pending.Delete(id)

	if err = c.Conn.WriteJSON(req); err != nil {
		// 写失败常因连接刚死;Done 已 signal 时归一化为 ErrConnClosed。
		select {
		case <-c.Conn.Done():
			return nil, ErrConnClosed
		default:
			return nil, err
		}
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, ErrConnClosed
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-c.Conn.Done():
		// 在飞期间连接断开:补 failPending 在 failPending→Conn.Close 两个 defer
		// 之间的竞态窗口,断连即时返回,不空等 CallTimeout。
		return nil, ErrConnClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) Notify(method string, params any) error {
	return c.Conn.SendNotification(method, params)
}

func (c *Client) Close() error {
	return c.Conn.Close()
}

func (c *Client) ReadLoop() {
	c.readLoop()
}

func (c *Client) readLoop() {
	defer c.Conn.Close()  // Ensure Done() is signaled when readLoop exits
	defer c.failPending() // 连接已死:唤醒所有在飞 Call
	for {
		req, resp, err := c.Conn.ReadMessage()
		if err != nil {
			c.logger.Debug("ws read error", zap.Error(err))
			return
		}

		if resp != nil && resp.ID != nil {
			if ch, ok := c.pending.Load(*resp.ID); ok {
				ch <- resp
			}
			continue
		}

		if req != nil && req.Method != "" {
			c.mu.RLock()
			handler, ok := c.handlers[req.Method]
			c.mu.RUnlock()
			if ok {
				go func(r *jsonrpc.Request) {
					result, herr := handler(context.Background(), r.Params)
					if r.ID != nil {
						if herr != nil {
							c.Conn.SendResponse(jsonrpc.NewErrorResponse(r.ID, jsonrpc.ErrInternal, herr.Error()))
						} else {
							rsp, _ := jsonrpc.NewResponse(r.ID, result)
							c.Conn.SendResponse(rsp)
						}
					}
				}(req)
			}
		}
	}
}

// failPending 关闭所有在飞 Call 的等待 channel,使其 <-ch 立即解除阻塞。
// 仅 readLoop 退出时调用;此后不再有 ch<-resp,关闭安全。
func (c *Client) failPending() {
	c.pending.Range(func(id int64, ch chan *jsonrpc.Response) bool {
		c.pending.Delete(id)
		close(ch)
		return true
	})
}
