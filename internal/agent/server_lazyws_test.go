package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
)

// TestLazyWSClient_ReturnsErrConnClosedWhenNotConnected 验证预连接窗口(client 尚未建立)
// 时 Call 返回 ws.ErrConnClosed,使上层 classifyResolveErr 能判成 master_unreachable
// 而非 unknown。
func TestLazyWSClient_ReturnsErrConnClosedWhenNotConnected(t *testing.T) {
	l := &lazyWSClient{getClient: func() *ws.Client { return nil }}
	_, err := l.Call(context.Background(), "any.method", nil)
	if !errors.Is(err, ws.ErrConnClosed) {
		t.Fatalf("got %v, want ws.ErrConnClosed", err)
	}
}
