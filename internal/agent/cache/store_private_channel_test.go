package cache

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// newTestStoreNoClient creates a Store with no WSClient (loaders nil).
// LRU writes still work via Set; reads with negative TTL still return nil on miss.
func newTestStoreNoClient(t *testing.T) *Store {
	t.Helper()
	return NewStore(nil, config.AgentCacheConfig{})
}

// setVisiblePrivateChannelsForTest is a test-only helper that bypasses the loader
// and writes directly to the LRU. Lives in this _test.go file so it isn't exported
// to production callers.
func setVisiblePrivateChannelsForTest(s *Store, userID uint, channels []protocol.SyncedPrivateChannel) {
	s.visiblePrivateChannels.Set(userID, &protocol.VisiblePrivateChannelSet{
		UserID:   userID,
		Channels: channels,
	})
}

func TestStore_GetVisiblePrivateChannelsForUser_HitAndFilterByModel(t *testing.T) {
	s := newTestStoreNoClient(t)
	setVisiblePrivateChannelsForTest(s, 1, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 10, Status: 1}, OwnerID: 1, Models: []string{"gpt-4o"}},
		{ChannelCore: models.ChannelCore{ID: 11, Status: 1}, OwnerID: 1, Models: []string{"claude-3-5-sonnet"}},
	})
	got := s.GetVisiblePrivateChannelsForUser(1, "gpt-4o")
	if len(got) != 1 || got[0].ID != 10 {
		t.Fatalf("filter by model failed: %+v", got)
	}
}

func TestStore_GetVisiblePrivateChannelsForUser_ZeroUser(t *testing.T) {
	s := newTestStoreNoClient(t)
	if got := s.GetVisiblePrivateChannelsForUser(0, "gpt-4o"); got != nil {
		t.Fatalf("user_id=0 should return nil: %+v", got)
	}
}

func TestStore_GetVisiblePrivateChannelsForUser_DisabledFiltered(t *testing.T) {
	s := newTestStoreNoClient(t)
	setVisiblePrivateChannelsForTest(s, 1, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 10, Status: 0}, OwnerID: 1, Models: []string{"gpt-4o"}},
	})
	if got := s.GetVisiblePrivateChannelsForUser(1, "gpt-4o"); len(got) != 0 {
		t.Fatalf("disabled should be filtered: %+v", got)
	}
}

func TestStore_GetVisiblePrivateChannelsForUser_CacheMissReturnsNil(t *testing.T) {
	s := newTestStoreNoClient(t)
	// no entry for user 1 and no loader → expect nil
	if got := s.GetVisiblePrivateChannelsForUser(1, "gpt-4o"); got != nil {
		t.Fatalf("cache miss with no loader should return nil: %+v", got)
	}
}

func TestStore_HandleSyncEvent_InvalidatesPrivateChannel(t *testing.T) {
	s := newTestStoreNoClient(t)
	setVisiblePrivateChannelsForTest(s, 1, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 10, Status: 1}, OwnerID: 1},
	})
	setVisiblePrivateChannelsForTest(s, 2, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 20, Status: 1}, OwnerID: 2},
	})

	payload, _ := json.Marshal(protocol.PrivateChannelInvalidatePayload{
		Action: "invalidate", AffectedUserIDs: []uint{1},
	})
	s.HandleSyncEvent(events.EntityPrivateChannel, "invalidate", payload)

	// user 1 cache should be gone; user 2 still there
	if got := s.GetVisiblePrivateChannelsForUser(1, "gpt-4o"); got != nil {
		t.Fatalf("user 1 cache not invalidated: %+v", got)
	}
	// user 2: getting with a non-matching model is fine; just verify invalidation didn't cascade
	set, _, _ := s.visiblePrivateChannels.Get(t.Context(), 2)
	if set == nil {
		t.Fatal("user 2 cache wrongly invalidated")
	}
}

func TestStore_HandleSyncEvent_InvalidatesShare(t *testing.T) {
	s := newTestStoreNoClient(t)
	setVisiblePrivateChannelsForTest(s, 7, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 99, Status: 1}, OwnerID: 1},
	})

	payload, _ := json.Marshal(protocol.PrivateChannelInvalidatePayload{
		Action: "invalidate", AffectedUserIDs: []uint{7},
	})
	s.HandleSyncEvent(events.EntityPrivateChannelShare, "invalidate", payload)

	if got := s.GetVisiblePrivateChannelsForUser(7, "gpt-4o"); got != nil {
		t.Fatalf("share invalidation didn't drop cache: %+v", got)
	}
}

type failingPrivLoader struct{}

func (failingPrivLoader) Load(_ context.Context, _ uint) (*protocol.VisiblePrivateChannelSet, error) {
	return nil, errors.New("rpc down")
}

func TestStore_GetVisiblePrivateChannels_LoaderErrorWarns(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	s := newTestStoreNoClient(t)
	s.SetLogger(zap.New(core))
	lru, err := entitycache.NewLRUCache(entitycache.Config[uint, *protocol.VisiblePrivateChannelSet]{
		Capacity: 8, Loader: failingPrivLoader{}, NegativeTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.visiblePrivateChannels = lru

	_ = s.GetVisiblePrivateChannelsForUser(42, "gpt-4o")
	// 行为变更:降级日志已统一为 "relay entity resolve degraded"(见 resolve_log.go),
	// 未分类错误归类 reason="unknown"。
	warns := logs.FilterMessage("relay entity resolve degraded").All()
	if len(warns) != 1 {
		t.Fatalf("expected 1 degrade warn, got %d", len(warns))
	}
	if warns[0].ContextMap()["entity"] != "private_channel_visible" {
		t.Errorf("entity want 'private_channel_visible', got %q", warns[0].ContextMap()["entity"])
	}
	if warns[0].ContextMap()["reason"] != "unknown" {
		t.Errorf("reason want 'unknown', got %q", warns[0].ContextMap()["reason"])
	}
}

func TestStore_HandleSyncEvent_LogsInvalidation(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	s := newTestStoreNoClient(t)
	s.SetLogger(zap.New(core))

	payload, _ := json.Marshal(protocol.PrivateChannelInvalidatePayload{
		Action: "invalidate", AffectedUserIDs: []uint{1, 2},
	})
	s.HandleSyncEvent(events.EntityPrivateChannel, "invalidate", payload)

	found := logs.FilterMessage("private channel invalidation received").All()
	if len(found) != 1 {
		t.Fatalf("expected 1 invalidation log, got %d", len(found))
	}
	if got := found[0].ContextMap()["affected_users"]; got != int64(2) {
		t.Fatalf("affected_users = %v, want 2", got)
	}
}
