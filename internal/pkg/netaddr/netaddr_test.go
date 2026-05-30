package netaddr

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

func TestParse(t *testing.T) {
	cases := []struct {
		in          string
		wantNetwork string
		wantAddress string
	}{
		{":8140", "tcp", ":8140"},
		{"127.0.0.1:8140", "tcp", "127.0.0.1:8140"},
		{"unix:/run/gw.sock", "unix", "/run/gw.sock"},
		{"unix:@gateway", "unix", "@gateway"},
		{"unix:", "unix", ""},
	}
	for _, c := range cases {
		gotNet, gotAddr := Parse(c.in)
		if gotNet != c.wantNetwork || gotAddr != c.wantAddress {
			t.Errorf("Parse(%q) = (%q,%q), want (%q,%q)", c.in, gotNet, gotAddr, c.wantNetwork, c.wantAddress)
		}
	}
}

func TestListen(t *testing.T) {
	// success: tcp
	tcpLn, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen: %v", err)
	}
	if tcpLn.Addr().Network() != "tcp" {
		t.Errorf("tcp network = %q", tcpLn.Addr().Network())
	}
	tcpLn.Close()

	// success: unix 文件型
	dir := t.TempDir()
	sock := filepath.Join(dir, "a.sock")
	ln, err := Listen("unix:" + sock)
	if err != nil {
		t.Fatalf("unix listen: %v", err)
	}
	ln.Close()

	// boundary: 普通文件残留应被拒绝（非 socket 不删）
	if err := os.WriteFile(sock, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen("unix:" + sock); err == nil {
		t.Errorf("expected refuse to remove non-socket file")
	}
	os.Remove(sock)

	// boundary: 真正的陈旧 socket 文件应被 unlink 后重新监听
	s2, _ := net.Listen("unix", sock)
	_ = s2 // 不 Close，文件残留
	ln2, err := Listen("unix:" + sock)
	if err != nil {
		t.Fatalf("relisten over stale socket: %v", err)
	}
	ln2.Close()

	// success: unix 抽象型（Linux）
	abs, err := Listen("unix:@netaddr-test-abs")
	if err != nil {
		t.Fatalf("abstract listen: %v", err)
	}
	abs.Close()
}

func TestSelfClient(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "pong")
	})

	// tcp
	tcpLn, _ := Listen("127.0.0.1:0")
	go http.Serve(tcpLn, mux)
	defer tcpLn.Close()
	tcpClient, tcpBase := SelfClient(tcpLn.Addr().String())
	if got := doGet(t, tcpClient, tcpBase+"/ping"); got != "pong" {
		t.Errorf("tcp self-call = %q", got)
	}

	// unix 文件型
	sock := filepath.Join(t.TempDir(), "self.sock")
	unixLn, _ := Listen("unix:" + sock)
	go http.Serve(unixLn, mux)
	defer unixLn.Close()
	unixClient, unixBase := SelfClient("unix:" + sock)
	if unixBase != "http://unix" {
		t.Errorf("unix base = %q, want http://unix", unixBase)
	}
	if got := doGet(t, unixClient, unixBase+"/ping"); got != "pong" {
		t.Errorf("unix self-call = %q", got)
	}

	// tcp: 原始配置串经 Parse tcp 分支取端口
	if _, base := SelfClient(":9999"); base != "http://127.0.0.1:9999" {
		t.Errorf("raw tcp base = %q, want http://127.0.0.1:9999", base)
	}
}

func TestWSDial(t *testing.T) {
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c.Close()
	})

	logger := zap.NewNop()

	// success: unix master_url 经 socket 握手成功
	sock := filepath.Join(t.TempDir(), "ws.sock")
	ln, _ := Listen("unix:" + sock)
	go http.Serve(ln, mux)
	defer ln.Close()
	cli, err := WSDial(context.Background(), "unix:"+sock, logger)
	if err != nil {
		t.Fatalf("unix WSDial: %v", err)
	}
	cli.Conn.Close()

	// success: http master_url 经 tcp 走 httpToWS 路径
	tcpLn, _ := Listen("127.0.0.1:0")
	go http.Serve(tcpLn, mux)
	defer tcpLn.Close()
	cli2, err := WSDial(context.Background(), "http://"+tcpLn.Addr().String(), logger)
	if err != nil {
		t.Fatalf("http WSDial: %v", err)
	}
	cli2.Conn.Close()

	// failure: 不存在的 socket 返回 error
	if _, err := WSDial(context.Background(), "unix:/no/such.sock", logger); err == nil {
		t.Errorf("expected error dialing missing socket")
	}
}

func doGet(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
