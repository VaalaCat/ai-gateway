package sync

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// fakeBroadcaster 捕获 Publisher 发出的广播。线程安全：MemoryBus 异步投递。
type fakeBroadcaster struct {
	mu     sync.Mutex
	pushes []protocol.SyncPushParams
}

func (f *fakeBroadcaster) Broadcast(_ string, params any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := params.(protocol.SyncPushParams); ok {
		f.pushes = append(f.pushes, p)
	}
}

func (f *fakeBroadcaster) snapshot() []protocol.SyncPushParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.SyncPushParams(nil), f.pushes...)
}

// waitFor 轮询等待 cond 成立（MemoryBus 异步投递 → 不能即时断言）。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 1s")
}

func TestPublisher_BroadcastsPrivateChannelInvalidate(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	fb := &fakeBroadcaster{}
	var version atomic.Int64
	p := &Publisher{hub: fb, bus: bus, version: &version, logger: zap.NewNop()}
	p.Start()

	if err := events.PublishPrivateChannelInvalidate(context.Background(), bus, []uint{38}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	waitFor(t, func() bool { return len(fb.snapshot()) > 0 })

	pushes := fb.snapshot()
	if len(pushes) != 1 {
		t.Fatalf("want 1 push, got %d", len(pushes))
	}
	if pushes[0].Entity != events.EntityPrivateChannel || pushes[0].Action != "invalidate" {
		t.Fatalf("unexpected push entity/action: %+v", pushes[0])
	}
	var payload protocol.PrivateChannelInvalidatePayload
	if err := json.Unmarshal(pushes[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if len(payload.AffectedUserIDs) != 1 || payload.AffectedUserIDs[0] != 38 {
		t.Fatalf("AffectedUserIDs = %v, want [38]", payload.AffectedUserIDs)
	}
}
