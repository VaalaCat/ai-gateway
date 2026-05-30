package cache

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// TestSyncer_PrivateChannelInvalidateViaPattern 复现 A2：经 bridge 路径把
// sync.private_channel.invalidate 发到本地 bus，syncer 须命中 pattern → 清缓存。
func TestSyncer_PrivateChannelInvalidateViaPattern(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	store := NewStore(nil, config.AgentCacheConfig{})
	setVisiblePrivateChannelsForTest(store, 38, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 5, Status: 1}, OwnerID: 38, Models: []string{"gpt-5.5"}},
	})

	syncer := &Syncer{Store: store, Bus: bus, Logger: zap.NewNop()}
	syncer.SubscribeEvents()

	payload, _ := json.Marshal(protocol.PrivateChannelInvalidatePayload{
		Action: events.ActionInvalidate, AffectedUserIDs: []uint{38},
	})
	push := protocol.SyncPushParams{
		Entity: events.EntityPrivateChannel, Action: events.ActionInvalidate, Data: payload, Version: 1,
	}
	if err := events.PublishSyncEvent(context.Background(), bus, push.Entity, push.Action, push); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// MemoryBus 异步投递 → 轮询等待缓存被清。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.GetVisiblePrivateChannelsForUser(38, "gpt-5.5") == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("user 38 private channel cache not invalidated via sync pattern")
}
