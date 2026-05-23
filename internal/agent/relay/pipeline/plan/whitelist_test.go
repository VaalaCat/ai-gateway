package plan

import (
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestFilterByAllowedChannels_Empty(t *testing.T) {
	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1}}, {ChannelCore: models.ChannelCore{ID: 2}}, {ChannelCore: models.ChannelCore{ID: 3}}}
	got := FilterByAllowedChannels(chs, nil)
	if !reflect.DeepEqual(got, chs) {
		t.Fatalf("nil allowed must return input unchanged; got %v", got)
	}
	got = FilterByAllowedChannels(chs, []uint{})
	if !reflect.DeepEqual(got, chs) {
		t.Fatalf("empty allowed must return input unchanged; got %v", got)
	}
}

func TestFilterByAllowedChannels_Subset(t *testing.T) {
	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1}}, {ChannelCore: models.ChannelCore{ID: 2}}, {ChannelCore: models.ChannelCore{ID: 3}}, {ChannelCore: models.ChannelCore{ID: 4}}}
	got := FilterByAllowedChannels(chs, []uint{2, 4})
	if len(got) != 2 || got[0].ID != 2 || got[1].ID != 4 {
		t.Fatalf("subset filter wrong; got %+v", got)
	}
}

func TestFilterByAllowedChannels_NoneMatch(t *testing.T) {
	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1}}, {ChannelCore: models.ChannelCore{ID: 2}}}
	got := FilterByAllowedChannels(chs, []uint{99})
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestFilterByAllowedChannels_GhostID(t *testing.T) {
	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1}}, {ChannelCore: models.ChannelCore{ID: 2}}}
	got := FilterByAllowedChannels(chs, []uint{1, 999, 2})
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("ghost ID not silently ignored; got %+v", got)
	}
}
