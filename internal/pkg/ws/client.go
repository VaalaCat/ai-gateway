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

type Client struct {
	Conn     *Conn
	pending  utils.SyncMap[int64, chan *jsonrpc.Response]
	nextID   atomic.Int64
	handlers map[string]app.NotificationHandler
	mu       sync.RWMutex
	logger   *zap.Logger
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
		Conn:     NewConn(ws, logger),
		handlers: make(map[string]app.NotificationHandler),
		logger:   logger,
	}
	go c.readLoop()
	return c, nil
}

func NewClientFromConn(conn *Conn) *Client {
	return &Client{
		Conn:     conn,
		handlers: make(map[string]app.NotificationHandler),
		logger:   conn.Logger,
	}
}

func (c *Client) OnNotification(method string, handler app.NotificationHandler) {
	c.mu.Lock()
	c.handlers[method] = handler
	c.mu.Unlock()
}

func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req, err := jsonrpc.NewRequest(method, params, &id)
	if err != nil {
		return nil, err
	}

	ch := make(chan *jsonrpc.Response, 1)
	c.pending.Store(id, ch)
	defer c.pending.Delete(id)

	err = c.Conn.WriteJSON(req)
	if err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
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
	defer c.Conn.Close() // Ensure Done() is signaled when readLoop exits
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
