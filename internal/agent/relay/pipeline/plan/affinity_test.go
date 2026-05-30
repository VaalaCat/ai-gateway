package plan

import (
	"testing"

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
	return &state.RelayContext{
		Input: state.RelayInput{UserInfo: &app.UserInfo{UserID: uid}},
		State: &state.RelayState{},
	}
}

func affCand(id uint, src state.ChannelSource) ScoredCandidate {
	return ScoredCandidate{Channel: &models.Channel{}, Source: src, SourceID: id}
}

func TestApplyAffinity_PromotesMatch(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	eng.Remember(affinity.Key{UserID: 1, RealModel: "m"}, state.SourceAdmin, 20)
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
	eng.Remember(affinity.Key{UserID: 1, RealModel: "m"}, state.SourceAdmin, 99)
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

func TestApplyAffinity_NilEngine(t *testing.T) {
	s := &defaultSolver{Affinity: nil}
	rctx := newAffRctx(1)
	in := []ScoredCandidate{affCand(10, state.SourceAdmin)}
	out := s.applyAffinity(rctx, "m", in)
	if len(out) != 1 || out[0].ByAffinity {
		t.Fatal("nil engine should be no-op")
	}
}
