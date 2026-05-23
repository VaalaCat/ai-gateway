package plan

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// newRctxForPool 用 stubAgentCache.channels 模拟 GetChannelsForModel 返回。
func newRctxForPool(channels []*models.Channel, ui *app.UserInfo, forced uint) *state.RelayContext {
	cache := &stubAgentCache{channels: channels}
	return newTestRelayContext(cache, "gpt-4", ui, forced)
}

// TestChannelPool_Basic: success — 无白名单 / 无 Forced，返回全部候选。
func TestChannelPool_Basic(t *testing.T) {
	ch := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled, Weight: 1}},
	}
	rctx := newRctxForPool(ch, &app.UserInfo{}, 0)

	got := newDefaultChannelPool().Available(rctx, "gpt-4")
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

// TestChannelPool_ForcedHit: success — Forced=2 命中 → 单元素 [2]。
func TestChannelPool_ForcedHit(t *testing.T) {
	ch := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}},
	}
	rctx := newRctxForPool(ch, &app.UserInfo{}, 2)

	got := newDefaultChannelPool().Available(rctx, "gpt-4")
	if len(got) != 1 || got[0].Channel.ID != 2 {
		t.Errorf("forced=2 → [2], got %v", got)
	}
}

// TestChannelPool_ForcedMiss: failure — Forced=999 未命中 → nil（让上层走 404）。
func TestChannelPool_ForcedMiss(t *testing.T) {
	ch := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}}}
	rctx := newRctxForPool(ch, &app.UserInfo{}, 999)

	got := newDefaultChannelPool().Available(rctx, "gpt-4")
	if got != nil {
		t.Errorf("forced miss → nil, got %v", got)
	}
}

// TestChannelPool_TokenWhitelist: success — Token 白名单只允许 ID=1。
func TestChannelPool_TokenWhitelist(t *testing.T) {
	ch := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}},
	}
	ui := &app.UserInfo{AllowedChannelIDs: []uint{1}}
	rctx := newRctxForPool(ch, ui, 0)

	got := newDefaultChannelPool().Available(rctx, "gpt-4")
	if len(got) != 1 || got[0].Channel.ID != 1 {
		t.Errorf("token whitelist [1] → [1], got %v", got)
	}
}

// TestChannelPool_GroupAndTokenIntersection: boundary — group ∩ token = AND，结果只剩 ID=2。
func TestChannelPool_GroupAndTokenIntersection(t *testing.T) {
	ch := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 3, Status: consts.StatusEnabled}},
	}
	ui := &app.UserInfo{
		GroupAllowedChannelIDs: []uint{1, 2},
		AllowedChannelIDs:      []uint{2, 3},
	}
	rctx := newRctxForPool(ch, ui, 0)

	got := newDefaultChannelPool().Available(rctx, "gpt-4")
	if len(got) != 1 || got[0].Channel.ID != 2 {
		t.Errorf("group[1,2] ∩ token[2,3] = [2], got %v", got)
	}
}

// TestChannelPool_Empty: boundary — 无候选 channel，返回空。
func TestChannelPool_Empty(t *testing.T) {
	rctx := newRctxForPool(nil, &app.UserInfo{}, 0)

	got := newDefaultChannelPool().Available(rctx, "gpt-4")
	if len(got) != 0 {
		t.Errorf("empty input → empty output, got %v", got)
	}
}

// TestChannelPool_NilUserInfo: boundary — UserInfo nil 不 panic，返回全部候选。
func TestChannelPool_NilUserInfo(t *testing.T) {
	ch := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}}}
	rctx := newRctxForPool(ch, nil, 0)

	got := newDefaultChannelPool().Available(rctx, "gpt-4")
	if len(got) != 1 || got[0].Channel.ID != 1 {
		t.Errorf("nil ui → all, got %v", got)
	}
}

// makeLister 把一组固定 channel 包成 channelLister 闭包，供 listers 扩展点测试用。
// 不依赖 rctx / realModel —— 直接返回预设切片，方便测"多 lister 拼接顺序"。
func makeLister(channels []*models.Channel) channelLister {
	return func(*state.RelayContext, string) []ScoredCandidate {
		out := make([]ScoredCandidate, 0, len(channels))
		for _, ch := range channels {
			out = append(out, ScoredCandidate{
				Channel:  ch,
				Source:   state.SourceAdmin,
				SourceID: ch.ID,
			})
		}
		return out
	}
}

// TestChannelPool_NoListers boundary：listers=nil 切片 → Available 不 panic 且返回空。
// 这是 BYOK 扩展点未注册 lister 时的兜底行为契约。
func TestChannelPool_NoListers(t *testing.T) {
	p := channelPoolImpl{listers: nil}
	rctx := newRctxForPool(nil, &app.UserInfo{}, 0)

	got := p.Available(rctx, "gpt-4")
	if len(got) != 0 {
		t.Errorf("nil listers → empty, got %v", got)
	}
}

// TestChannelPool_MultipleListersAppendOrder success：两个 lister 各返回不同 channel，
// 断言 Available 输出顺序 = lister1 结果 + lister2 结果（按 listers 切片顺序拼接）。
// BYOK 上线后 personal/shared 共存时这是顺序契约。
func TestChannelPool_MultipleListersAppendOrder(t *testing.T) {
	l1 := makeLister([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 11, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 12, Status: consts.StatusEnabled}},
	})
	l2 := makeLister([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 21, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 22, Status: consts.StatusEnabled}},
	})
	p := channelPoolImpl{listers: []channelLister{l1, l2}}
	rctx := newRctxForPool(nil, &app.UserInfo{}, 0)

	got := p.Available(rctx, "gpt-4")
	wantIDs := []uint{11, 12, 21, 22}
	if len(got) != len(wantIDs) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(wantIDs), got)
	}
	for i, want := range wantIDs {
		if got[i].Channel.ID != want {
			t.Errorf("idx %d: got ID=%d want %d", i, got[i].Channel.ID, want)
		}
	}
}

// TestChannelPool_ListerReturnsNil boundary：单 lister 返回 nil → 不 panic，结果为空。
// nil 切片对 append 透明（channel_pool.go:54 注释明示）。
func TestChannelPool_ListerReturnsNil(t *testing.T) {
	p := channelPoolImpl{listers: []channelLister{makeLister(nil)}}
	rctx := newRctxForPool(nil, &app.UserInfo{}, 0)

	got := p.Available(rctx, "gpt-4")
	if len(got) != 0 {
		t.Errorf("lister nil → empty, got %v", got)
	}
}

// TestChannelPool_MixedNilAndNonNilListers boundary：多 lister 部分返回 nil + 部分返回 channels。
// 验证 nil lister 不破坏拼接顺序，非 nil 部分按序保留。
func TestChannelPool_MixedNilAndNonNilListers(t *testing.T) {
	l1 := makeLister(nil)
	l2 := makeLister([]*models.Channel{{ChannelCore: models.ChannelCore{ID: 5, Status: consts.StatusEnabled}}})
	l3 := makeLister(nil)
	l4 := makeLister([]*models.Channel{{ChannelCore: models.ChannelCore{ID: 9, Status: consts.StatusEnabled}}})
	p := channelPoolImpl{listers: []channelLister{l1, l2, l3, l4}}
	rctx := newRctxForPool(nil, &app.UserInfo{}, 0)

	got := p.Available(rctx, "gpt-4")
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d (%v)", len(got), got)
	}
	if got[0].Channel.ID != 5 || got[1].Channel.ID != 9 {
		t.Errorf("order broken: got %v want [5,9]", got)
	}
}

// TestChannelPool_ListersWithForcedID 扩展点 + ForcedChannelID 协同：
// listers 共出 5 个 channel，ForcedID 指定其中 1 个 → 返回该 1 个。
func TestChannelPool_ListersWithForcedID(t *testing.T) {
	l1 := makeLister([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}},
	})
	l2 := makeLister([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 3, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 4, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 5, Status: consts.StatusEnabled}},
	})
	p := channelPoolImpl{listers: []channelLister{l1, l2}}
	rctx := newRctxForPool(nil, &app.UserInfo{}, 4)

	got := p.Available(rctx, "gpt-4")
	if len(got) != 1 || got[0].Channel.ID != 4 {
		t.Errorf("ForcedID=4 ∩ listers → [4], got %v", got)
	}
}

// TestChannelPool_ListersWithTokenWhitelist 扩展点 + Token 白名单协同：
// listers 共出 5 个 channel，AllowedChannelIDs=[2,4] → 返回交集 [2,4]。
func TestChannelPool_ListersWithTokenWhitelist(t *testing.T) {
	l1 := makeLister([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}},
	})
	l2 := makeLister([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 3, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 4, Status: consts.StatusEnabled}},
		{ChannelCore: models.ChannelCore{ID: 5, Status: consts.StatusEnabled}},
	})
	p := channelPoolImpl{listers: []channelLister{l1, l2}}
	ui := &app.UserInfo{AllowedChannelIDs: []uint{2, 4}}
	rctx := newRctxForPool(nil, ui, 0)

	got := p.Available(rctx, "gpt-4")
	if len(got) != 2 {
		t.Fatalf("expected 2 (2,4), got %d (%v)", len(got), got)
	}
	if got[0].Channel.ID != 2 || got[1].Channel.ID != 4 {
		t.Errorf("whitelist ∩ listers = [2,4]，order preserved，got %v", got)
	}
}
