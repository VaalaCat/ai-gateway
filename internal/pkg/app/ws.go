package app

import (
	"context"
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
)

// NotificationHandler 处理 JSON-RPC 通知的回调函数。
type NotificationHandler func(ctx context.Context, params json.RawMessage) (any, error)

// WSClient WebSocket 客户端
// 封装 WebSocket 连接，提供 JSON-RPC 风格的 Call（请求-响应）和 Notify（单向通知）能力
type WSClient interface {
	// OnNotification 注册指定方法的通知回调处理器
	OnNotification(method string, handler NotificationHandler)
	// Call 发起 RPC 调用并等待响应
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	// Notify 发送单向通知（不等待响应）
	Notify(method string, params any) error
	// Close 关闭 WebSocket 连接
	Close() error
	// ReadLoop 启动消息读取循环（通常在 goroutine 中运行）
	ReadLoop()
}

// WSConn WebSocket 服务端连接
// 封装单个 WebSocket 连接的服务端视角，提供消息读写和生命周期管理
type WSConn interface {
	// SendNotification 向客户端发送 JSON-RPC 通知
	SendNotification(method string, params any) error
	// SendResponse 向客户端发送 JSON-RPC 响应
	SendResponse(resp *jsonrpc.Response) error
	// ReadMessage 读取一条消息，区分请求和响应
	ReadMessage() (*jsonrpc.Request, *jsonrpc.Response, error)
	// WriteJSON 发送任意 JSON 数据
	WriteJSON(v any) error
	// Close 关闭连接
	Close() error
	// Done 返回连接关闭的通知 channel
	Done() <-chan struct{}
}
