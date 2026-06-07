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
