package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"go.uber.org/zap"
)

func TestClientServerRoundTrip(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		for {
			req, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if req != nil && req.Method == "echo" && req.ID != nil {
				resp, _ := jsonrpc.NewResponse(req.ID, json.RawMessage(req.Params))
				conn.SendResponse(resp)
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	client, err := Dial(context.Background(), wsURL, logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.Call(ctx, "echo", map[string]string{"msg": "hello"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}

	var parsed map[string]string
	json.Unmarshal(result, &parsed)
	if parsed["msg"] != "hello" {
		t.Errorf("got %v, want hello", parsed["msg"])
	}
}

func TestNotification(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	received := make(chan string, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SendNotification("ping", map[string]string{"msg": "pong"})
		time.Sleep(500 * time.Millisecond)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	client, err := Dial(context.Background(), wsURL, logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	client.OnNotification("ping", func(ctx context.Context, params json.RawMessage) (any, error) {
		received <- string(params)
		return nil, nil
	})

	select {
	case msg := <-received:
		if !strings.Contains(msg, "pong") {
			t.Errorf("got %s, want pong", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestInlineNotificationDispatchBlocksReaderUntilHandlerReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{}
	client.BindNotificationContext(ctx)
	entered := make(chan struct{})
	release := make(chan struct{})
	client.OnNotificationInline("bounded", func(context.Context, json.RawMessage) (any, error) {
		close(entered)
		<-release
		return nil, nil
	})
	req, err := jsonrpc.NewNotification("bounded", nil)
	if err != nil {
		t.Fatalf("new notification: %v", err)
	}
	dispatchDone := make(chan struct{})
	go func() {
		client.dispatchNotification(req)
		close(dispatchDone)
	}()
	<-entered
	select {
	case <-dispatchDone:
		t.Fatal("inline dispatch returned before its bounded handler")
	default:
	}
	close(release)
	<-dispatchDone
}

func TestNotificationContextUsesBoundLifecycleAndCancelsOnDisconnect(t *testing.T) {
	type contextKey struct{}
	const contextValue = "agent-server-lifecycle"
	logger := zap.NewNop()
	sendNotification := make(chan struct{})
	closeServerConn := make(chan struct{})
	serverReady := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		defer conn.Close()
		close(serverReady)
		<-sendNotification
		if err := conn.SendNotification("lifecycle", nil); err != nil {
			return
		}
		<-closeServerConn
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	<-serverReady
	parentCtx, cancelParent := context.WithCancel(context.WithValue(context.Background(), contextKey{}, contextValue))
	defer cancelParent()
	client.BindNotificationContext(parentCtx)
	handlerCtx := make(chan context.Context, 1)
	client.OnNotificationInline("lifecycle", func(ctx context.Context, _ json.RawMessage) (any, error) {
		handlerCtx <- ctx
		return nil, nil
	})
	close(sendNotification)

	var gotCtx context.Context
	select {
	case gotCtx = <-handlerCtx:
	case <-time.After(5 * time.Second):
		t.Fatal("notification handler did not run")
	}
	if got := gotCtx.Value(contextKey{}); got != contextValue {
		t.Fatalf("notification context value = %v, want %q", got, contextValue)
	}
	close(closeServerConn)
	select {
	case <-client.Conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client connection did not close")
	}
	select {
	case <-gotCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("notification context was not cancelled on disconnect")
	}
}

func TestTransportCloseJoinsNotificationHandlers(t *testing.T) {
	logger := zap.NewNop()
	send := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		defer conn.Close()
		<-send
		_ = conn.SendNotification("block", nil)
		<-conn.Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client, err := Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	client.OnNotification("block", func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		return nil, nil
	})
	close(send)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("notification handler did not start")
	}
	closeDone := make(chan struct{})
	go func() {
		_ = client.Close()
		<-client.Done()
		close(closeDone)
	}()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel notification context")
	}
	select {
	case <-closeDone:
		t.Fatal("Close returned before notification handler exited")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not join notification handler")
	}
}

func TestLifecycleNotificationHandlerCanCloseClientWithoutSelfJoin(t *testing.T) {
	logger := zap.NewNop()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SendNotification("close", nil)
		<-conn.Done()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client, err := Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	returned := make(chan struct{})
	client.OnNotification("close", func(context.Context, json.RawMessage) (any, error) {
		_ = client.Close()
		close(returned)
		return nil, nil
	})
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("notification handler self-joined in Client.Close")
	}
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("Client.Done did not join reader and notification handler")
	}
}

func TestReadLoopClosesConnOnError(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		// Immediately close server side to trigger client read error
		conn.Close()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	client, err := Dial(context.Background(), wsURL, logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	select {
	case <-client.Conn.Done():
		// Success: Done() was signaled after readLoop detected error
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: Conn.Done() not signaled after server closed connection")
	}
}

func TestCallRespectsContextTimeout(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		defer conn.Close()
		// Read but never respond — simulate hung server
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	client, err := Dial(context.Background(), wsURL, logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = client.Call(ctx, "noop", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	// Should timeout around 200ms, not 30s
	if elapsed > 2*time.Second {
		t.Errorf("Call took %v, expected ~200ms timeout", elapsed)
	}
}

func TestCallReturnsCustomContextCause(t *testing.T) {
	logger := zap.NewNop()
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", logger)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("caller stopped RPC")
	result := make(chan error, 1)
	go func() {
		_, err := client.Call(ctx, "noop", nil)
		result <- err
	}()
	cancel(cause)
	select {
	case err := <-result:
		if !errors.Is(err, cause) {
			t.Fatalf("Call = %v, want custom cause %v", err, cause)
		}
	case <-time.After(time.Second):
		t.Fatal("Call remained blocked after custom cancellation")
	}
}

func TestCallTimesOutWithoutCallerDeadline(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		defer conn.Close()
		for { // 读但永不回包,保持连接存活
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	client, err := Dial(context.Background(), wsURL, logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	client.CallTimeout = 200 * time.Millisecond

	start := time.Now()
	_, err = client.Call(context.Background(), "noop", nil) // 无 caller deadline
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 2*time.Second {
		t.Errorf("Call took %v, expected ~200ms CallTimeout", elapsed)
	}
}

// TestCallRespectsLongerCallerDeadlineThanCallTimeout 锁定 CallTimeout 是兜底而非上限:
// 调用方显式传入的 deadline 比 c.CallTimeout 更长时,不应被 CallTimeout 提前截断。
func TestCallRespectsLongerCallerDeadlineThanCallTimeout(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		defer conn.Close()
		// 用读循环而非"读一次就 return"：让 defer conn.Close() 只在客户端断开
		// (测试收尾 client.Close())时触发，避免 handler 提前返回和 writeLoop
		// 异步落盘响应之间的收尾竞态（同 TestClientServerRoundTrip 的模式）。
		for {
			req, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if req != nil && req.Method == "echo" && req.ID != nil {
				time.Sleep(150 * time.Millisecond) // 比 CallTimeout(50ms) 长,但比 caller deadline(500ms) 短
				resp, _ := jsonrpc.NewResponse(req.ID, json.RawMessage(req.Params))
				conn.SendResponse(resp)
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	client, err := Dial(context.Background(), wsURL, logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	client.CallTimeout = 50 * time.Millisecond // 刻意收窄,验证不会当作上限套用

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result, err := client.Call(ctx, "echo", map[string]string{"msg": "hello"})
	if err != nil {
		t.Fatalf("call: %v, want success (caller deadline should not be capped by CallTimeout)", err)
	}

	var parsed map[string]string
	json.Unmarshal(result, &parsed)
	if parsed["msg"] != "hello" {
		t.Errorf("got %v, want hello", parsed["msg"])
	}
}

func TestCallFailsFastWhenConnAlreadyClosed(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		conn.Close() // server 立即断开
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	client, err := Dial(context.Background(), wsURL, logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// 等连接确实死掉(readLoop 退出 → Conn.Done 已 signal)。
	select {
	case <-client.Conn.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: 连接未在预期内断开")
	}

	// 连接已死后才发起的 Call:应归一化为 ErrConnClosed(便于上层
	// classifyResolveErr 判 master_unreachable),而非底层 websocket 写错误。
	client.CallTimeout = 30 * time.Second
	start := time.Now()
	_, callErr := client.Call(context.Background(), "noop", nil)
	elapsed := time.Since(start)

	if !errors.Is(callErr, ErrConnClosed) {
		t.Fatalf("got %v, want ErrConnClosed", callErr)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Call 耗时 %v,连接已死应立即返回", elapsed)
	}
}

func TestCallReturnsWhenConnDropsMidFlight(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, logger)
		if err != nil {
			return
		}
		conn.ReadMessage() // 收下在飞请求但不回包
		conn.Close()       // 在飞时断连
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	client, err := Dial(context.Background(), wsURL, logger)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		// context.Background():无 deadline。修复前此调用永久挂。
		_, callErr := client.Call(context.Background(), "noop", nil)
		done <- callErr
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrConnClosed) {
			t.Fatalf("got %v, want ErrConnClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Call 在连接断开后仍挂住(根因 A 未修)")
	}
}
