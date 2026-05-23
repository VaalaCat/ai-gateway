package cache

import (
	"encoding/json"
	"testing"

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
