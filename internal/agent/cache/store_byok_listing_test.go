package cache

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestStore_ListVisibleBYOKModelNamesForUser_HitMergeDedup(t *testing.T) {
	s := newTestStoreNoClient(t)
	setVisiblePrivateChannelsForTest(s, 1, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 10, Status: 1}, OwnerID: 1, Models: []string{"gpt-4o", "gpt-4o-mini"}},
		{ChannelCore: models.ChannelCore{ID: 11, Status: 1}, OwnerID: 1, Models: []string{"gpt-4o", "claude-3-5-sonnet"}},
	})
	got := s.ListVisibleBYOKModelNamesForUser(1)
	want := []string{"gpt-4o", "gpt-4o-mini", "claude-3-5-sonnet"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q (got=%v)", i, got[i], w, got)
		}
	}
}

func TestStore_ListVisibleBYOKModelNamesForUser_ZeroUser(t *testing.T) {
	s := newTestStoreNoClient(t)
	if got := s.ListVisibleBYOKModelNamesForUser(0); got != nil {
		t.Fatalf("user_id=0 should return nil: %+v", got)
	}
}

func TestStore_ListVisibleBYOKModelNamesForUser_CacheMissReturnsNil(t *testing.T) {
	s := newTestStoreNoClient(t)
	if got := s.ListVisibleBYOKModelNamesForUser(1); got != nil {
		t.Fatalf("cache miss with no loader should return nil: %+v", got)
	}
}

func TestStore_ListVisibleBYOKModelNamesForUser_DisabledFiltered(t *testing.T) {
	s := newTestStoreNoClient(t)
	setVisiblePrivateChannelsForTest(s, 1, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 10, Status: 1}, OwnerID: 1, Models: []string{"gpt-4o"}},
		{ChannelCore: models.ChannelCore{ID: 11, Status: 0}, OwnerID: 1, Models: []string{"gpt-5"}},
	})
	got := s.ListVisibleBYOKModelNamesForUser(1)
	if len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("disabled channel should be filtered: got=%v, want [gpt-4o]", got)
	}
}

func TestStore_ListVisibleBYOKModelNamesForUser_EmptyChannelsReturnsNil(t *testing.T) {
	s := newTestStoreNoClient(t)
	setVisiblePrivateChannelsForTest(s, 1, []protocol.SyncedPrivateChannel{})
	if got := s.ListVisibleBYOKModelNamesForUser(1); got != nil {
		t.Fatalf("empty set should return nil, got=%v", got)
	}
}
