package sync

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// fakeBroadcaster 捕获 Publisher 发出的广播与定向通知。线程安全：MemoryBus 异步投递。
type fakeBroadcaster struct {
	mu       sync.Mutex
	pushes   []protocol.SyncPushParams
	notifies []fakeNotify
}

// fakeNotify 记录一次 NotifyAgent 调用。
type fakeNotify struct {
	agentID string
	method  string
	params  any
}

func (f *fakeBroadcaster) Broadcast(_ string, params any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := params.(protocol.SyncPushParams); ok {
		f.pushes = append(f.pushes, p)
	}
}

func (f *fakeBroadcaster) NotifyAgent(agentID, method string, params any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifies = append(f.notifies, fakeNotify{agentID: agentID, method: method, params: params})
}

func (f *fakeBroadcaster) snapshot() []protocol.SyncPushParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.SyncPushParams(nil), f.pushes...)
}

func (f *fakeBroadcaster) notifySnapshot() []fakeNotify {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeNotify(nil), f.notifies...)
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

func TestPublisher_RoutesUserQuotaSyncToSourceAgent(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	fb := &fakeBroadcaster{}
	var version atomic.Int64
	p := &Publisher{hub: fb, bus: bus, version: &version, logger: zap.NewNop()}
	p.Start()

	want := protocol.UserQuotaSync{
		AgentID: "agent-7",
		Users:   []protocol.SyncedUser{{ID: 3, GroupID: 1, Quota: 50}},
	}
	if err := events.PublishUserQuotaSync(context.Background(), bus, want); err != nil {
		t.Fatalf("publish: %v", err)
	}

	waitFor(t, func() bool { return len(fb.notifySnapshot()) > 0 })

	notifies := fb.notifySnapshot()
	if len(notifies) != 1 {
		t.Fatalf("want 1 notify, got %d", len(notifies))
	}
	n := notifies[0]
	if n.agentID != "agent-7" {
		t.Fatalf("agentID = %q, want agent-7", n.agentID)
	}
	if n.method != consts.RPCSyncUserQuota {
		t.Fatalf("method = %q, want %q", n.method, consts.RPCSyncUserQuota)
	}
	users, ok := n.params.([]protocol.SyncedUser)
	if !ok {
		t.Fatalf("params type = %T, want []protocol.SyncedUser", n.params)
	}
	if len(users) != 1 || users[0].ID != 3 || users[0].Quota != 50 {
		t.Fatalf("users payload = %+v, want [{ID:3 ... Quota:50}]", users)
	}

	// 不应触发全量 Broadcast。
	if got := len(fb.snapshot()); got != 0 {
		t.Fatalf("Broadcast count = %d, want 0 (targeted notify only)", got)
	}
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
