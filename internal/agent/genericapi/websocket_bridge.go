package genericapi

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gorilla/websocket"
	"github.com/sourcegraph/conc/pool"
)

const webSocketControlWriteTimeout = 5 * time.Second

const webSocketInternalErrorReason = "websocket bridge failed"

// WebSocketBridge copies one committed client connection and one provider
// connection in both directions. It owns no retry or reconnect policy.
type WebSocketBridge struct{ findControlWriteTimeout func() time.Duration }

func (b WebSocketBridge) withControlWriteTimeout(find func() time.Duration) WebSocketBridge {
	b.findControlWriteTimeout = find
	return b
}

func (b WebSocketBridge) controlWriteTimeout() time.Duration {
	if b.findControlWriteTimeout == nil {
		return webSocketControlWriteTimeout
	}
	return b.findControlWriteTimeout()
}

func controlWriteDeadline(timeout time.Duration) time.Time {
	if timeout <= 0 {
		return time.Time{}
	}
	return time.Now().Add(timeout)
}

type WebSocketBridgeResult struct{ CloseCode int }

type webSocketEventStream interface {
	SendEvent(context.Context, app.WebSocketEvent) error
	ReceiveEvent(context.Context) (app.WebSocketEvent, error)
}

func (b WebSocketBridge) Connections(ctx context.Context, client, upstream *websocket.Conn) error {
	_, err := b.ConnectionsWithResult(ctx, client, upstream)
	return err
}

func (b WebSocketBridge) ConnectionsWithResult(
	ctx context.Context,
	client, upstream *websocket.Conn,
) (WebSocketBridgeResult, error) {
	if ctx == nil || client == nil || upstream == nil {
		return WebSocketBridgeResult{}, ErrExecutionUnavailable
	}
	var closeCode atomic.Int32
	observeClose := func(code int) {
		if code > 0 {
			closeCode.CompareAndSwap(0, int32(code))
		}
	}
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = client.Close()
			_ = upstream.Close()
		})
	}
	timeout := b.controlWriteTimeout()
	installWebSocketControlForwarders(client, upstream, observeClose, timeout)
	installWebSocketControlForwarders(upstream, client, observeClose, timeout)
	stopClose := context.AfterFunc(ctx, closeBoth)
	defer func() {
		stopClose()
		closeBoth()
	}()

	workers := pool.New().WithContext(ctx).WithCancelOnError().WithFirstError()
	workers.Go(func(context.Context) error {
		err := copyWebSocketMessages(upstream, client)
		err = notifyWebSocketConnectionOnBridgeError(upstream, err, timeout)
		closeBoth()
		return err
	})
	workers.Go(func(context.Context) error {
		err := copyWebSocketMessages(client, upstream)
		err = notifyWebSocketConnectionOnBridgeError(client, err, timeout)
		closeBoth()
		return err
	})
	err := workers.Wait()
	if err != nil {
		observeClose(websocket.CloseInternalServerErr)
	}
	return WebSocketBridgeResult{CloseCode: int(closeCode.Load())}, err
}

// ConnectionAndStream bridges a source-side Gorilla connection to the typed
// cross-Agent stream without changing message or control-frame boundaries.
func (b WebSocketBridge) ConnectionAndStream(ctx context.Context, client *websocket.Conn, stream app.WebSocketAPIStream) error {
	_, err := b.ConnectionAndStreamWithResult(ctx, client, stream)
	return err
}

func (b WebSocketBridge) ConnectionAndStreamWithResult(
	ctx context.Context,
	client *websocket.Conn,
	stream app.WebSocketAPIStream,
) (WebSocketBridgeResult, error) {
	// The source handler owns stream.Close after it drains FrameAPIResult.
	return b.connectionAndEventStream(ctx, client, stream, nil)
}

func (b WebSocketBridge) connectionAndEventStream(
	ctx context.Context,
	client *websocket.Conn,
	stream webSocketEventStream,
	closeStream func() error,
) (WebSocketBridgeResult, error) {
	if ctx == nil || client == nil || stream == nil {
		return WebSocketBridgeResult{}, ErrExecutionUnavailable
	}
	var closeCode atomic.Int32
	observeClose := func(code int) {
		if code > 0 {
			closeCode.CompareAndSwap(0, int32(code))
		}
	}
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = client.Close()
			if closeStream != nil {
				_ = closeStream()
			}
		})
	}
	timeout := b.controlWriteTimeout()
	installWebSocketStreamControlForwarders(ctx, client, stream, observeClose, timeout)
	stopClose := context.AfterFunc(ctx, closeBoth)
	defer func() {
		stopClose()
		closeBoth()
	}()

	workers := pool.New().WithContext(ctx).WithCancelOnError().WithFirstError()
	workers.Go(func(workerCtx context.Context) error {
		err := copyWebSocketConnectionToStream(workerCtx, stream, client)
		err = notifyWebSocketStreamOnBridgeError(stream, err, timeout)
		closeBoth()
		return err
	})
	workers.Go(func(workerCtx context.Context) error {
		err := copyWebSocketStreamToConnection(workerCtx, client, stream, observeClose, timeout)
		err = notifyWebSocketConnectionOnBridgeError(client, err, timeout)
		closeBoth()
		return err
	})
	err := workers.Wait()
	if err != nil {
		observeClose(websocket.CloseInternalServerErr)
	}
	return WebSocketBridgeResult{CloseCode: int(closeCode.Load())}, err
}

func notifyWebSocketConnectionOnBridgeError(connection *websocket.Conn, err error, timeout time.Duration) error {
	err = normalizeWebSocketBridgeError(err)
	if err == nil {
		return nil
	}
	_ = connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseInternalServerErr, webSocketInternalErrorReason),
		controlWriteDeadline(timeout),
	)
	return err
}

func notifyWebSocketStreamOnBridgeError(stream webSocketEventStream, err error, timeout time.Duration) error {
	err = normalizeWebSocketBridgeError(err)
	if err == nil {
		return nil
	}
	notifyCtx, cancel := context.WithCancel(context.Background())
	if timeout > 0 {
		notifyCtx, cancel = context.WithTimeout(context.Background(), timeout)
	}
	defer cancel()
	_ = stream.SendEvent(notifyCtx, app.WebSocketEvent{
		Kind: app.WebSocketCloseEvent, Code: websocket.CloseInternalServerErr, Reason: webSocketInternalErrorReason,
	})
	return err
}

func copyWebSocketConnectionToStream(ctx context.Context, target webSocketEventStream, source *websocket.Conn) error {
	var messageID uint64
	buffer := make([]byte, 32<<10)
	for {
		messageType, reader, err := source.NextReader()
		if err != nil {
			return err
		}
		messageID++
		typed, err := webSocketMessageType(messageType)
		if err != nil {
			return err
		}
		if err = target.SendEvent(ctx, app.WebSocketEvent{Kind: app.WebSocketMessageStartEvent, MessageID: messageID, Type: typed}); err != nil {
			return err
		}
		for {
			count, readErr := reader.Read(buffer)
			if count > 0 {
				if err = target.SendEvent(ctx, app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: messageID, Data: append([]byte(nil), buffer[:count]...)}); err != nil {
					return err
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		if err = target.SendEvent(ctx, app.WebSocketEvent{Kind: app.WebSocketMessageEndEvent, MessageID: messageID}); err != nil {
			return err
		}
	}
}

func copyWebSocketStreamToConnection(
	ctx context.Context,
	target *websocket.Conn,
	source webSocketEventStream,
	observeClose func(int),
	timeout time.Duration,
) error {
	var writer io.WriteCloser
	var messageID uint64
	for {
		event, err := source.ReceiveEvent(ctx)
		if err != nil {
			return err
		}
		switch event.Kind {
		case app.WebSocketMessageStartEvent:
			if writer != nil || event.MessageID == 0 || event.MessageID <= messageID {
				return ErrExecutionUnavailable
			}
			messageType, typeErr := gorillaWebSocketMessageType(event.Type)
			if typeErr != nil {
				return typeErr
			}
			writer, err = target.NextWriter(messageType)
			if err != nil {
				return err
			}
			messageID = event.MessageID
		case app.WebSocketMessageDataEvent:
			if writer == nil || event.MessageID != messageID || len(event.Data) == 0 {
				return ErrExecutionUnavailable
			}
			if _, err = writer.Write(event.Data); err != nil {
				return err
			}
		case app.WebSocketMessageEndEvent:
			if writer == nil || event.MessageID != messageID {
				return ErrExecutionUnavailable
			}
			err, writer = writer.Close(), nil
			if err != nil {
				return err
			}
		case app.WebSocketPingEvent, app.WebSocketPongEvent:
			messageType := websocket.PingMessage
			if event.Kind == app.WebSocketPongEvent {
				messageType = websocket.PongMessage
			}
			if err = target.WriteControl(messageType, event.Data, controlWriteDeadline(timeout)); err != nil {
				return err
			}
		case app.WebSocketCloseEvent:
			observeClose(event.Code)
			return target.WriteControl(
				websocket.CloseMessage, websocket.FormatCloseMessage(event.Code, event.Reason),
				controlWriteDeadline(timeout),
			)
		default:
			return ErrExecutionUnavailable
		}
	}
}

func installWebSocketStreamControlForwarders(
	ctx context.Context,
	source *websocket.Conn,
	target webSocketEventStream,
	observeClose func(int),
	timeout time.Duration,
) {
	forward := func(event app.WebSocketEvent) error {
		var sendCtx context.Context
		var cancel context.CancelFunc
		if timeout > 0 {
			sendCtx, cancel = context.WithTimeout(ctx, timeout)
		} else {
			sendCtx, cancel = context.WithCancel(ctx)
		}
		defer cancel()
		return target.SendEvent(sendCtx, event)
	}
	source.SetPingHandler(func(data string) error {
		return forward(app.WebSocketEvent{Kind: app.WebSocketPingEvent, Data: []byte(data)})
	})
	source.SetPongHandler(func(data string) error {
		return forward(app.WebSocketEvent{Kind: app.WebSocketPongEvent, Data: []byte(data)})
	})
	source.SetCloseHandler(func(code int, reason string) error {
		observeClose(code)
		return forward(app.WebSocketEvent{Kind: app.WebSocketCloseEvent, Code: code, Reason: reason})
	})
}

func webSocketMessageType(messageType int) (int, error) {
	switch messageType {
	case websocket.TextMessage:
		return app.WebSocketTextMessage, nil
	case websocket.BinaryMessage:
		return app.WebSocketBinaryMessage, nil
	default:
		return 0, ErrExecutionUnavailable
	}
}

func gorillaWebSocketMessageType(messageType int) (int, error) {
	switch messageType {
	case app.WebSocketTextMessage:
		return websocket.TextMessage, nil
	case app.WebSocketBinaryMessage:
		return websocket.BinaryMessage, nil
	default:
		return 0, ErrExecutionUnavailable
	}
}

func copyWebSocketMessages(target, source *websocket.Conn) error {
	for {
		messageType, reader, err := source.NextReader()
		if err != nil {
			return err
		}
		writer, err := target.NextWriter(messageType)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, reader)
		closeErr := writer.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
}

func installWebSocketControlForwarders(source, target *websocket.Conn, observeClose func(int), timeout time.Duration) {
	forward := func(messageType int) func(string) error {
		return func(data string) error {
			return target.WriteControl(messageType, []byte(data), controlWriteDeadline(timeout))
		}
	}
	source.SetPingHandler(forward(websocket.PingMessage))
	source.SetPongHandler(forward(websocket.PongMessage))
	source.SetCloseHandler(func(code int, reason string) error {
		observeClose(code)
		return target.WriteControl(
			websocket.CloseMessage, websocket.FormatCloseMessage(code, reason),
			controlWriteDeadline(timeout),
		)
	})
}

func normalizeWebSocketBridgeError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
		return nil
	}
	return err
}
