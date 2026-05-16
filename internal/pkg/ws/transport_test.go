package ws

import (
	"context"
	"encoding/json"
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
