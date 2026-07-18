package ws

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
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
	// httptest.Server.Close 不会关掉已 hijack 走的 websocket 连接(hijack 后
	// net/http 就不再追踪其生命周期),serverConn.writeLoop 若不显式 Close 会在
	// 测试函数返回后继续存活。多个测试都会临时改包级 SendQueueSize/WriteDeadline,
	// 遗留的 writeLoop 复用旧值读这两个 var 就会跟下一个测试的写并发 → data race。
	// 这里兜底 Close，确保当前测试的 writeLoop 在下一个测试跑之前已经退出。
	t.Cleanup(func() { serverConn.Close() })
	return serverConn, clientConn, srv
}

// shrinkSocketBuffers 把 server/client 两端底层 TCP socket 的收发 buffer 都缩到
// 内核允许的最小值，让"队列/连接堆满"在几条消息内就确定性发生——不再依赖机器/内核
// 当次的 auto-tuning 结果（真实 TCP buffer 从几百 KB 到几 MB 不等，是旧版
// TestConn_SendQueueFullClosesConn ~1/3 概率 flake 的根因）。同时保留一个不小的 payload
// (bigPayload)，两者叠加双保险，几条消息内必定把内核 buffer 撑满。
func shrinkSocketBuffers(t *testing.T, serverConn *Conn, clientConn *websocket.Conn) {
	t.Helper()
	if tc, ok := serverConn.WS.UnderlyingConn().(*net.TCPConn); ok {
		tc.SetWriteBuffer(1024)
	}
	if tc, ok := clientConn.UnderlyingConn().(*net.TCPConn); ok {
		tc.SetReadBuffer(1024)
	}
}

// bigPayload 生成一段填充串，配合 shrinkSocketBuffers 让 TCP buffer 在几条消息内堆满，
// 避免测试依赖内核 buffer 的真实容量（不可预测 → flake）。
func bigPayload(n int) string {
	return strings.Repeat("x", n)
}

func TestSendNotificationDropsWhenQueueFull(t *testing.T) {
	oldSize := SendQueueSize
	SendQueueSize = 1
	defer func() { SendQueueSize = oldSize }()

	conn, client, srv := upgradeForTest(t) // client 侧故意不读,writeLoop 很快堵死
	defer srv.Close()
	shrinkSocketBuffers(t, conn, client)

	// 填满队列后继续发通知:应返回 errSendQueueFull 且连接不关闭
	var lastErr error
	for i := 0; i < 50; i++ {
		lastErr = conn.SendNotification("sync.push", map[string]any{"i": i, "pad": bigPayload(2048)})
		if lastErr != nil {
			break
		}
	}
	if lastErr == nil {
		t.Fatal("expected drop error once queue full")
	}
	if lastErr != errSendQueueFull {
		t.Fatalf("expected errSendQueueFull, got %v", lastErr)
	}
	select {
	case <-conn.Done():
		t.Fatal("droppable overflow must NOT close the conn")
	default:
	}
}

func TestSendResponseBackpressureThenCloses(t *testing.T) {
	oldSize, oldDl := SendQueueSize, WriteDeadline
	SendQueueSize, WriteDeadline = 1, 100*time.Millisecond
	defer func() { SendQueueSize, WriteDeadline = oldSize, oldDl }()

	conn, client, srv := upgradeForTest(t)
	defer srv.Close()
	shrinkSocketBuffers(t, conn, client)

	id := int64(1)
	resp, _ := jsonrpc.NewResponse(&id, map[string]any{"ok": true, "pad": bigPayload(2048)})
	// 灌到底:关键路径先背压等待,超时后才关连接
	start := time.Now()
	var err error
	for i := 0; i < 50; i++ {
		if err = conn.SendResponse(resp); err != nil {
			break
		}
	}
	if err == nil {
		t.Fatal("expected error after sustained backpressure")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("closed too fast (%v): critical path must wait WriteDeadline first", elapsed)
	}
	select {
	case <-conn.Done():
	default:
		t.Fatal("conn should be closed after critical-path deadline exceeded")
	}
}

func TestSendNotificationNormalPathUnaffected(t *testing.T) { // success/回归
	conn, client, srv := upgradeForTest(t)
	defer srv.Close()
	if err := conn.SendNotification("sync.push", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("normal send failed: %v", err)
	}
	client.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := client.ReadMessage(); err != nil {
		t.Fatalf("client did not receive notification: %v", err)
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
