package netaddr

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// unixPrefix 标识 Unix Domain Socket 地址。前导 @ 表示 Linux 抽象命名空间 socket。
const unixPrefix = "unix:"

// Parse 把配置字符串解析为 net 包用的 (network, address)。
// "unix:/x" → ("unix","/x")；"unix:@x" → ("unix","@x")；其余 → ("tcp", addr)。
func Parse(addr string) (network, address string) {
	if path, ok := strings.CutPrefix(addr, unixPrefix); ok {
		return "unix", path
	}
	return "tcp", addr
}

// Listen 按 Parse 结果监听。文件型 unix 在监听前清理陈旧 socket 文件
// （仅当残留文件确实是一个 socket 时才删，避免误删普通文件）。
// 抽象型 unix（前导 @）与 tcp 直接监听。
func Listen(addr string) (net.Listener, error) {
	network, address := Parse(addr)
	if network == "unix" && !strings.HasPrefix(address, "@") {
		if err := removeStaleSocket(address); err != nil {
			return nil, err
		}
	}
	return net.Listen(network, address)
}

func removeStaleSocket(path string) error {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("netaddr: refusing to remove non-socket file %q", path)
	}
	return os.Remove(path)
}

// WSDial 拨 master 的 ws RPC 端点 /ws/agent。
//
//	masterURL 以 unix: 开头 → 占位 ws://unix/ws/agent + NetDialContext 拨该 socket
//	http(s)/ws(s)          → 转成 ws(s):// 并补 /ws/agent 后用默认 dialer
func WSDial(ctx context.Context, masterURL string, logger *zap.Logger, headers ...http.Header) (*ws.Client, error) {
	if strings.HasPrefix(masterURL, unixPrefix) {
		_, address := Parse(masterURL)
		// unix socket: 无需 KeepAlive（仅对 TCP 有意义）
		dialer := &websocket.Dialer{
			HandshakeTimeout: 45 * time.Second,
			NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, "unix", address)
			},
		}
		return ws.DialWithDialer(ctx, dialer, "ws://unix/ws/agent", logger, headers...)
	}
	dialer := &websocket.Dialer{
		HandshakeTimeout: 45 * time.Second,
		NetDialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	return ws.DialWithDialer(ctx, dialer, httpToWS(masterURL), logger, headers...)
}

// httpToWS 把 master URL 规整为 ws(s):// 且确保以 /ws/agent 结尾。
func httpToWS(raw string) string {
	u := raw
	u = strings.Replace(u, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	u = strings.TrimRight(u, "/")
	if !strings.HasSuffix(u, "/ws/agent") {
		u += "/ws/agent"
	}
	return u
}

// UnixHTTPClient returns an http.Client whose every request is routed to the
// unix socket named by a "unix:" address (e.g. "unix:/run/gw.sock" or
// "unix:@name"). Pair it with base URL "http://unix" — the host is ignored.
func UnixHTTPClient(addr string) *http.Client {
	_, address := Parse(addr)
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", address)
			},
		},
	}
}

// SelfClient 返回回拨「自己 listen 地址」用的 http.Client 与 base URL。
//
//	unix → client 的 DialContext 固定拨该 socket，base = "http://unix"
//	tcp  → 普通 client，base = "http://127.0.0.1:<port>"
func SelfClient(addr string) (*http.Client, string) {
	network, address := Parse(addr)
	if network == "unix" {
		return UnixHTTPClient(addr), "http://unix"
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		port = address
	}
	return &http.Client{Timeout: 30 * time.Second}, fmt.Sprintf("http://127.0.0.1:%s", port)
}
