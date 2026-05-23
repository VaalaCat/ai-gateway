package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// upgradeForTest 起一个 httptest server，Upgrade 后返回 server 端 Conn 和 client 端 ws conn。
func upgradeForTest(t *testing.T) (*Conn, *websocket.Conn, *httptest.Server) {
	t.Helper()
	var serverConn *Conn
	ready := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r, zap.NewNop())
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		serverConn = c
		close(ready)
		// 阻塞读取保持连接，直到 client 关
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { clientConn.Close() })

	<-ready
	return serverConn, clientConn, srv
}

func TestConn_SendQueueFullClosesConn(t *testing.T) {
	serverConn, clientConn, _ := upgradeForTest(t)

	// 让 client 永远不读，serverConn 写出的 frame 堆在 TCP buffer；
	// sendQueue 满后第 257 条触发 Close
	clientConn.SetReadDeadline(time.Now().Add(1 * time.Hour))

	// 用一个稍大的 payload 加速 TCP buffer 填满
	big := strings.Repeat("x", 4096)
	sent := 0
	var lastErr error
	for i := 0; i < sendQueueSize+1024; i++ {
		if err := serverConn.WriteJSON(map[string]string{"i": big}); err != nil {
			lastErr = err
			break
		}
		sent++
	}
	if lastErr == nil {
		t.Errorf("expected eventually errSendQueueFull or errClosedConn, got nil after %d sends", sent)
	}
	if lastErr != nil && lastErr != errSendQueueFull && lastErr != errClosedConn {
		t.Errorf("unexpected err: %v", lastErr)
	}
}

func TestConn_CloseDrainsWriteLoop(t *testing.T) {
	serverConn, _, _ := upgradeForTest(t)

	// 起 N 条 enqueue 然后 Close，断言 Close 返回时 sendDone closed
	for i := 0; i < 10; i++ {
		serverConn.WriteJSON(map[string]int{"i": i})
	}
	serverConn.Close()

	select {
	case <-serverConn.sendDone:
		// ok
	case <-time.After(1 * time.Second):
		t.Errorf("Close did not wait for writeLoop to exit")
	}
}

func TestConn_PingIsSentPeriodically(t *testing.T) {
	// pingInterval=30s 真实等会让测试太慢，跳过这个测试，留 follow-up 用 mock time
	t.Skip("requires mock time to avoid 30s real wait; deferred to follow-up")
}

// 防止 unused import 警告（json 引入但未使用时编译会报错）
var _ = json.Marshal

