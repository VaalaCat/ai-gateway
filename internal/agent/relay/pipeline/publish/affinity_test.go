package publish

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/affinity"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

type affStubCfg struct{ on int }

func (c affStubCfg) Settings() settings.AgentSettings {
	return settings.AgentSettings{AffinityEnabled: c.on, AffinityTTLSec: 300}
}

func affRctx(used state.Attempt, out state.AttemptResult, hadEntry bool) *state.RelayContext {
	return &state.RelayContext{
		Input: state.RelayInput{UserInfo: &app.UserInfo{UserID: 1}},
		State: &state.RelayState{
			Plan:      state.AttemptPlan{HadAffinityEntry: hadEntry},
			Execution: state.ExecutionResult{Used: used, Outcome: out},
		},
	}
}

func TestFillExecution_AffinityHit(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	p := NewPublisher(nil, nil, eng)
	used := state.Attempt{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5, ByAffinity: true}
	rctx := affRctx(used, state.AttemptResult{CacheReadTokens: 100}, true)
	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)
	p.recordAffinity(rctx, &e)
	if e.AffinityStatus != "hit" {
		t.Fatalf("AffinityStatus = %q, want hit", e.AffinityStatus)
	}
	if !e.AffinityRecorded {
		t.Fatal("cache_read>0 success should record affinity")
	}
	if _, ok := eng.Lookup(affinity.Key{UserID: 1, RealModel: "m"}); !ok {
		t.Fatal("engine should hold the recorded entry")
	}
}

func TestFillExecution_AffinityFallback(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	p := NewPublisher(nil, nil, eng)
	used := state.Attempt{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5, ByAffinity: false}
	rctx := affRctx(used, state.AttemptResult{}, true)
	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)
	p.recordAffinity(rctx, &e)
	if e.AffinityStatus != "fallback" {
		t.Fatalf("AffinityStatus = %q, want fallback", e.AffinityStatus)
	}
	if e.AffinityRecorded {
		t.Fatal("no cache activity should not record")
	}
}

func TestFillExecution_AffinityNone(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	p := NewPublisher(nil, nil, eng)
	used := state.Attempt{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5}
	rctx := affRctx(used, state.AttemptResult{}, false)
	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)
	p.recordAffinity(rctx, &e)
	if e.AffinityStatus != "none" {
		t.Fatalf("AffinityStatus = %q, want none", e.AffinityStatus)
	}
}

func TestFillExecution_AffinityRecordIsolatedByTokenID(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	p := NewPublisher(nil, nil, eng)
	used := state.Attempt{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5}
	rctx := affRctx(used, state.AttemptResult{CacheWriteTokens: 100}, false)
	rctx.Input.UserInfo.TokenID = 11
	var entry protocol.UsageLogEntry
	projectExecution(&entry, rctx)
	p.recordAffinity(rctx, &entry)

	if _, ok := eng.Lookup(affinity.Key{UserID: 1, TokenID: 11, RealModel: "m"}); !ok {
		t.Fatal("current token should own the recorded affinity entry")
	}
	for _, tokenID := range []uint{0, 22} {
		if _, ok := eng.Lookup(affinity.Key{UserID: 1, TokenID: tokenID, RealModel: "m"}); ok {
			t.Fatalf("token %d must not share the current token affinity entry", tokenID)
		}
	}
}

func TestFillExecution_SessionIdentityRecordsFirstSuccessfulRequest(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	p := NewPublisher(nil, nil, eng)
	used := state.Attempt{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5}
	rctx := affRctx(used, state.AttemptResult{}, false)
	rctx.Input.UserInfo.TokenID = 11
	rctx.Input.AffinityIdentity = state.AffinityIdentity{
		Key:       "session-a",
		Partition: state.AffinityPartition{ByUser: true, ByToken: true, ByModel: true},
	}
	var entry protocol.UsageLogEntry
	projectExecution(&entry, rctx)
	p.recordAffinity(rctx, &entry)

	if !entry.AffinityRecorded {
		t.Fatal("successful request with a session identity should record affinity without cache token usage")
	}
	key := affinity.BuildKey(rctx.Input.AffinityIdentity, 1, 11, "m")
	got, ok := eng.Lookup(key)
	if !ok || got.SourceID != 5 {
		t.Fatalf("session affinity lookup = (%+v, %v), want source 5", got, ok)
	}
}

func TestFillExecution_SessionIdentityDoesNotRecordFailedRequest(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	p := NewPublisher(nil, nil, eng)
	used := state.Attempt{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5}
	rctx := affRctx(used, state.AttemptResult{}, false)
	rctx.Input.AffinityIdentity = state.AffinityIdentity{
		Key:       "session-a",
		Partition: state.AffinityPartition{ByUser: true, ByToken: true, ByModel: true},
	}
	rctx.State.Execution.Err = errors.New("upstream failed")
	var entry protocol.UsageLogEntry
	projectExecution(&entry, rctx)
	p.recordAffinity(rctx, &entry)

	if entry.AffinityRecorded {
		t.Fatal("failed request must not record session affinity")
	}
	key := affinity.BuildKey(rctx.Input.AffinityIdentity, 1, 0, "m")
	if _, ok := eng.Lookup(key); ok {
		t.Fatal("failed request unexpectedly stored session affinity")
	}
}

func TestFillExecution_AffinityDisabledEmpty(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 0})
	p := NewPublisher(nil, nil, eng)
	used := state.Attempt{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5, ByAffinity: true}
	rctx := affRctx(used, state.AttemptResult{CacheReadTokens: 100}, true)
	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)
	p.recordAffinity(rctx, &e)
	if e.AffinityStatus != "" || e.AffinityRecorded {
		t.Fatalf("disabled should leave status empty / no record, got %q / %v", e.AffinityStatus, e.AffinityRecorded)
	}
}

func TestFillExecution_AffinityHitOnFailure(t *testing.T) {
	eng := affinity.New(affStubCfg{on: 1})
	p := NewPublisher(nil, nil, eng)
	used := state.Attempt{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5, ByAffinity: true}
	rctx := affRctx(used, state.AttemptResult{}, true)
	rctx.State.Execution.Err = errors.New("upstream failed") // make fillExecution treat it as failure
	var e protocol.UsageLogEntry
	projectExecution(&e, rctx)
	p.recordAffinity(rctx, &e)
	if e.AffinityStatus != affinity.StatusHit {
		t.Fatalf("status on failure = %q, want hit", e.AffinityStatus)
	}
	if e.AffinityRecorded {
		t.Fatal("failed request must not record affinity")
	}
}
