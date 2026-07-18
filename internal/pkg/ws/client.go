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
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

var _ app.WSClient = (*Client)(nil)

// defaultCallTimeout 是 Call 的兜底超时(连接存活但对端不回包时生效)。
const defaultCallTimeout = 30 * time.Second

type Client struct {
	Conn           *Conn
	pending        utils.SyncMap[int64, chan *jsonrpc.Response]
	nextID         atomic.Int64
	handlers       map[string]app.NotificationHandler
	inlineHandlers map[string]app.NotificationHandler
	mu             sync.RWMutex
	logger         *zap.Logger

	notificationCtx    context.Context
	notificationCancel context.CancelFunc
	notificationClosed atomic.Bool
	handlerMu          sync.Mutex
	handlersClosed     bool
	handlerWorkers     conc.WaitGroup
	readOnce           sync.Once
	closeOnce          sync.Once
	done               chan struct{}
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
	c := newClient(NewConn(ws, logger), logger)
	c.startReadLoop()
	return c, nil
}

func NewClientFromConn(conn *Conn) *Client {
	return newClient(conn, conn.Logger)
}

func newClient(conn *Conn, logger *zap.Logger) *Client {
	notificationCtx, notificationCancel := context.WithCancel(context.Background())
	return &Client{
		Conn:               conn,
		handlers:           make(map[string]app.NotificationHandler),
		inlineHandlers:     make(map[string]app.NotificationHandler),
		logger:             logger,
		CallTimeout:        defaultCallTimeout,
		notificationCtx:    notificationCtx,
		notificationCancel: notificationCancel,
		done:               make(chan struct{}),
	}
}

func (c *Client) OnNotification(method string, handler app.NotificationHandler) {
	c.mu.Lock()
	if c.handlers == nil {
		c.handlers = make(map[string]app.NotificationHandler)
	}
	c.handlers[method] = handler
	delete(c.inlineHandlers, method)
	c.mu.Unlock()
}

// OnNotificationInline registers a bounded, non-blocking handler that runs on
// the reader goroutine. Other notification handlers retain asynchronous dispatch.
func (c *Client) OnNotificationInline(method string, handler app.NotificationHandler) {
	c.mu.Lock()
	if c.inlineHandlers == nil {
		c.inlineHandlers = make(map[string]app.NotificationHandler)
	}
	c.inlineHandlers[method] = handler
	delete(c.handlers, method)
	c.mu.Unlock()
}

// BindNotificationContext binds future notification handlers to the owning
// server lifecycle. The context is also cancelled when this connection exits.
func (c *Client) BindNotificationContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.TODO()
	}
	nextCtx, nextCancel := context.WithCancel(ctx)
	c.mu.Lock()
	previousCancel := c.notificationCancel
	c.notificationCtx = nextCtx
	c.notificationCancel = nextCancel
	closed := c.notificationClosed.Load()
	c.mu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	if closed {
		nextCancel()
	}
}

func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// CallTimeout 是兜底,不是上限:子 context 的 deadline 只能收紧不能放宽,
	// 若无条件套用会把调用方显式传入的更长 deadline(如 settings 驱动、可配置
	// 5-300s 的心跳超时)悄悄截断到 CallTimeout。只在调用方没给 deadline 时才补上。
	if c.CallTimeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.CallTimeout)
			defer cancel()
		}
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
		return nil, context.Cause(ctx)
	}
}

func (c *Client) Notify(method string, params any) error {
	return c.Conn.SendNotification(method, params)
}

func (c *Client) Close() error {
	c.requestClose()
	return nil
}

func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) ReadLoop() {
	c.startReadLoop()
	<-c.Done()
}

func (c *Client) startReadLoop() {
	c.readOnce.Do(func() { c.handlerWorkers.Go(c.readLoop) })
}

func (c *Client) readLoop() {
	defer c.requestClose()
	defer c.failPending() // 连接已死:唤醒所有在飞 Call
	defer func() {
		c.notificationClosed.Store(true)
		c.closeHandlerAdmission()
		c.cancelNotificationContext()
	}()
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
			c.dispatchNotification(req)
		}
	}
}

func (c *Client) requestClose() {
	c.closeOnce.Do(func() {
		c.notificationClosed.Store(true)
		c.closeHandlerAdmission()
		c.cancelNotificationContext()
		go c.finalizeClose()
	})
}

func (c *Client) finalizeClose() {
	_ = c.Conn.Close()
	c.handlerWorkers.Wait()
	close(c.done)
}

func (c *Client) dispatchNotification(req *jsonrpc.Request) {
	c.mu.RLock()
	inlineHandler, inline := c.inlineHandlers[req.Method]
	handler, ok := c.handlers[req.Method]
	ctx := c.notificationCtx
	c.mu.RUnlock()
	if inline {
		c.runNotificationHandler(ctx, inlineHandler, req)
		return
	}
	if ok {
		c.startNotificationHandler(ctx, handler, req)
	}
}

func (c *Client) startNotificationHandler(ctx context.Context, handler app.NotificationHandler, req *jsonrpc.Request) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	if c.handlersClosed {
		return
	}
	c.handlerWorkers.Go(func() { c.runNotificationHandler(ctx, handler, req) })
}

func (c *Client) closeHandlerAdmission() {
	c.handlerMu.Lock()
	c.handlersClosed = true
	c.handlerMu.Unlock()
}

func (c *Client) runNotificationHandler(ctx context.Context, handler app.NotificationHandler, req *jsonrpc.Request) {
	if ctx == nil {
		ctx = context.TODO()
	}
	result, err := handler(ctx, req.Params)
	if req.ID == nil {
		return
	}
	if err != nil {
		c.Conn.SendResponse(jsonrpc.NewErrorResponse(req.ID, jsonrpc.ErrInternal, err.Error()))
		return
	}
	resp, _ := jsonrpc.NewResponse(req.ID, result)
	c.Conn.SendResponse(resp)
}

func (c *Client) cancelNotificationContext() {
	c.mu.RLock()
	cancel := c.notificationCancel
	c.mu.RUnlock()
	if cancel != nil {
		cancel()
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
