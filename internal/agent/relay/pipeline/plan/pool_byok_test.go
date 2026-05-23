package plan

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestPrivateChannelsVisible_NilContext(t *testing.T) {
	if got := privateChannelsVisibleToCaller(nil, "gpt-4o"); got != nil {
		t.Fatalf("nil context should return nil, got %+v", got)
	}
}

func TestPrivateChannelsVisible_NilUserInfo(t *testing.T) {
	rctx := &state.RelayContext{Input: state.RelayInput{UserInfo: nil}}
	if got := privateChannelsVisibleToCaller(rctx, "gpt-4o"); got != nil {
		t.Fatalf("nil UserInfo should return nil, got %+v", got)
	}
}

func TestPrivateChannelsVisible_ZeroUserID(t *testing.T) {
	rctx := &state.RelayContext{
		Input: state.RelayInput{UserInfo: &app.UserInfo{UserID: 0}},
	}
	if got := privateChannelsVisibleToCaller(rctx, "gpt-4o"); got != nil {
		t.Fatalf("UserID=0 should return nil, got %+v", got)
	}
}

// TestPrivateChannelsVisible_ReturnsSourcePrivate: success path — cache returns one
// private channel; lister wraps it as SourcePrivate with priority offset applied.
func TestPrivateChannelsVisible_ReturnsSourcePrivate(t *testing.T) {
	pc := &protocol.SyncedPrivateChannel{ChannelCore: models.ChannelCore{ID: 42, Type: 1, Status: 1, Priority: 5, Weight: 1}, KeyPlaintext: "sk-private", Models: []string{"gpt-4o"}}
	cache := &stubAgentCache{
		channels: nil, // sharedChannels not called here
		privChannels: map[string][]*protocol.SyncedPrivateChannel{
			"gpt-4o": {pc},
		},
	}
	rctx := newTestRelayContext(cache, "gpt-4o", &app.UserInfo{UserID: 7}, 0)

	got := privateChannelsVisibleToCaller(rctx, "gpt-4o")
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}
	sc := got[0]
	if sc.Source != state.SourcePrivate {
		t.Errorf("Source should be SourcePrivate, got %v", sc.Source)
	}
	if sc.SourceID != 42 {
		t.Errorf("SourceID should be 42, got %d", sc.SourceID)
	}
	if sc.Channel.Key != "sk-private" {
		t.Errorf("Key should be sk-private, got %q", sc.Channel.Key)
	}
	// Task 15: priority 直接透传，不再叠加 +10000 offset；
	// "private 优先 + shared 兜底" 由 sort.go 的 source rank 二级排序保证。
	if sc.Channel.Priority != 5 {
		t.Errorf("Priority should be passed through unchanged (5), got %d", sc.Channel.Priority)
	}
}
