package plan

import (
	"testing"

	"gorm.io/datatypes"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/affinity"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

type affStubCfg struct{ on int }

func (c affStubCfg) Settings() settings.AgentSettings {
	return settings.AgentSettings{AffinityEnabled: c.on, AffinityTTLSec: 300}
}

func newAffRctx(uid uint) *state.RelayContext {
	return newAffTokenRctx(uid, 0)
}

func newAffTokenRctx(uid, tokenID uint) *state.RelayContext {
	return &state.RelayContext{
		Input: state.RelayInput{UserInfo: &app.UserInfo{UserID: uid, TokenID: tokenID}},
		State: &state.RelayState{},
	}
}

func affCand(id uint, src state.ChannelSource) ScoredCandidate {
	return ScoredCandidate{Channel: &models.Channel{}, Source: src, SourceID: id}
}

func TestApplyAffinity_PromotesMatch(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	eng.Remember(affinity.Key{UserID: 1, RealModel: "m"}, state.SourceAdmin, 20, nil)
	s := &defaultSolver{Affinity: eng}
	rctx := newAffRctx(1)
	in := []ScoredCandidate{affCand(10, state.SourceAdmin), affCand(20, state.SourceAdmin), affCand(30, state.SourceAdmin)}
	out := s.applyAffinity(rctx, "m", in)
	if out[0].SourceID != 20 || !out[0].ByAffinity {
		t.Fatalf("want id=20 ByAffinity at front, got id=%d ByAffinity=%v", out[0].SourceID, out[0].ByAffinity)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 candidates preserved, got %d", len(out))
	}
	if !rctx.State.Plan.HadAffinityEntry {
		t.Fatal("HadAffinityEntry should be set")
	}
}

func TestApplyAffinity_EntryExistsButNotInPool(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	eng.Remember(affinity.Key{UserID: 1, RealModel: "m"}, state.SourceAdmin, 99, nil)
	s := &defaultSolver{Affinity: eng}
	rctx := newAffRctx(1)
	in := []ScoredCandidate{affCand(10, state.SourceAdmin)}
	out := s.applyAffinity(rctx, "m", in)
	if out[0].SourceID != 10 || out[0].ByAffinity {
		t.Fatal("no matching candidate should leave order unchanged")
	}
	if !rctx.State.Plan.HadAffinityEntry {
		t.Fatal("HadAffinityEntry should be set even when channel filtered (fallback case)")
	}
}

func TestApplyAffinity_Miss(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	s := &defaultSolver{Affinity: eng}
	rctx := newAffRctx(1)
	in := []ScoredCandidate{affCand(10, state.SourceAdmin)}
	out := s.applyAffinity(rctx, "m", in)
	if out[0].SourceID != 10 || rctx.State.Plan.HadAffinityEntry {
		t.Fatal("miss should leave order unchanged and HadAffinityEntry false")
	}
}

func TestApplyAffinity_IsolatesByTokenID(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	eng.Remember(affinity.Key{UserID: 1, TokenID: 11, RealModel: "m"}, state.SourceAdmin, 20, nil)
	s := &defaultSolver{Affinity: eng}
	in := []ScoredCandidate{affCand(10, state.SourceAdmin), affCand(20, state.SourceAdmin)}

	t.Run("same token hits", func(t *testing.T) {
		rctx := newAffTokenRctx(1, 11)
		out := s.applyAffinity(rctx, "m", in)
		if out[0].SourceID != 20 || !out[0].ByAffinity || !rctx.State.Plan.HadAffinityEntry {
			t.Fatalf("same token should hit source 20, got source=%d by_affinity=%v had_entry=%v",
				out[0].SourceID, out[0].ByAffinity, rctx.State.Plan.HadAffinityEntry)
		}
	})

	t.Run("different token misses", func(t *testing.T) {
		rctx := newAffTokenRctx(1, 22)
		out := s.applyAffinity(rctx, "m", in)
		if out[0].SourceID != 10 || out[0].ByAffinity || rctx.State.Plan.HadAffinityEntry {
			t.Fatalf("different token should miss, got source=%d by_affinity=%v had_entry=%v",
				out[0].SourceID, out[0].ByAffinity, rctx.State.Plan.HadAffinityEntry)
		}
	})
}

func TestApplyAffinity_NilEngine(t *testing.T) {
	s := &defaultSolver{Affinity: nil}
	rctx := newAffRctx(1)
	in := []ScoredCandidate{affCand(10, state.SourceAdmin)}
	out := s.applyAffinity(rctx, "m", in)
	if len(out) != 1 || out[0].ByAffinity {
		t.Fatal("nil engine should be no-op")
	}
}

// affCandWithOverride builds a ScoredCandidate with a per-channel Affinity override.
func affCandWithOverride(id uint, src state.ChannelSource, ovr models.ChannelAffinity) ScoredCandidate {
	ch := &models.Channel{}
	ch.Affinity = datatypes.NewJSONType(ovr)
	return ScoredCandidate{Channel: ch, Source: src, SourceID: id}
}

// TestApplyAffinity_ChannelForcedDisabled_ForgetsAndSkips: hit + channel force-disabled →
// not reranked, record Forgotten (Lookup now misses), HadAffinityEntry == false.
func TestApplyAffinity_ChannelForcedDisabled_ForgetsAndSkips(t *testing.T) {
	fls := false
	eng := affinity.New(affStubCfg{on: 1}) // global on
	key := affinity.Key{UserID: 1, RealModel: "m"}
	eng.Remember(key, state.SourceAdmin, 20, nil)
	s := &defaultSolver{Affinity: eng}
	rctx := newAffRctx(1)
	// c1 (id=20) has force-disabled override; c2 (id=10) is plain
	in := []ScoredCandidate{
		affCand(10, state.SourceAdmin),
		affCandWithOverride(20, state.SourceAdmin, models.ChannelAffinity{Enabled: &fls}),
	}
	out := s.applyAffinity(rctx, "m", in)
	// should NOT be reranked: id=10 stays at front
	if out[0].SourceID != 10 {
		t.Fatalf("want id=10 at front (not reranked), got id=%d", out[0].SourceID)
	}
	// record must be Forgotten
	if _, exists := eng.Lookup(key); exists {
		t.Fatal("record should have been Forgotten after force-disabled channel hit")
	}
	// HadAffinityEntry must NOT be set (self-heal → None)
	if rctx.State.Plan.HadAffinityEntry {
		t.Fatal("HadAffinityEntry should be false after self-heal Forget")
	}
}

// TestApplyAffinity_GlobalOffChannelForcedOn_Hits: global-off + channel force-enabled +
// record exists → reranked to top, ByAffinity == true, HadAffinityEntry == true.
func TestApplyAffinity_GlobalOffChannelForcedOn_Hits(t *testing.T) {
	tru := true
	eng := affinity.New(affStubCfg{on: 0}) // global OFF
	key := affinity.Key{UserID: 1, RealModel: "m"}
	eng.Remember(key, state.SourceAdmin, 20, nil)
	s := &defaultSolver{Affinity: eng}
	rctx := newAffRctx(1)
	in := []ScoredCandidate{
		affCand(10, state.SourceAdmin),
		affCandWithOverride(20, state.SourceAdmin, models.ChannelAffinity{Enabled: &tru}),
	}
	out := s.applyAffinity(rctx, "m", in)
	// c1 (id=20) should be promoted to front
	if out[0].SourceID != 20 {
		t.Fatalf("want id=20 at front (force-enabled), got id=%d", out[0].SourceID)
	}
	if !out[0].ByAffinity {
		t.Fatal("ByAffinity should be true on promoted candidate")
	}
	if !rctx.State.Plan.HadAffinityEntry {
		t.Fatal("HadAffinityEntry should be true on hit")
	}
}

// TestApplyAffinity_RecordedChannelMissing_Fallback: recorded channel not in candidates →
// HadAffinityEntry == true, order unchanged.
func TestApplyAffinity_RecordedChannelMissing_Fallback(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	eng.Remember(affinity.Key{UserID: 1, RealModel: "m"}, state.SourceAdmin, 999, nil)
	s := &defaultSolver{Affinity: eng}
	rctx := newAffRctx(1)
	in := []ScoredCandidate{affCand(10, state.SourceAdmin), affCand(20, state.SourceAdmin)}
	out := s.applyAffinity(rctx, "m", in)
	// order must be unchanged
	if out[0].SourceID != 10 || out[1].SourceID != 20 {
		t.Fatalf("want order [10,20] unchanged, got [%d,%d]", out[0].SourceID, out[1].SourceID)
	}
	// HadAffinityEntry must be true (fallback)
	if !rctx.State.Plan.HadAffinityEntry {
		t.Fatal("HadAffinityEntry should be true on fallback (record exists but channel missing)")
	}
}
