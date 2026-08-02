package rpc

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/publish"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/stretchr/testify/require"
)

func TestHandleInflight_OmitsAutoDisableTriggers(t *testing.T) {
	reg := inflight.NewRegistry(nil, 0)
	e := reg.Track(inflight.Meta{ReqID: "triggered"})
	defer e.Done()
	e.Update(publish.ProjectInflightEntry(&state.RelayContext{
		Input: state.RelayInput{RequestID: "triggered", StartTime: time.Now()},
		State: &state.RelayState{
			FailPhase: state.PhaseCtxBuild,
			AutoDisableTriggers: []attemptproxy.ChannelAutoDisableTrigger{{
				Source: attemptproxy.SourceAdmin, ChannelID: 7, Revision: 4,
				Reason: attemptproxy.ChannelAutoDisableReasonConsecutiveErrors,
			}},
		},
	}))

	res, err := HandleInflight(reg)
	require.NoError(t, err)
	payload, err := json.Marshal(res)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"triggered"`)
	require.NotContains(t, string(payload), `"auto_disable_triggers"`)
}

func TestHandleInflight_ReturnsSnapshot(t *testing.T) {
	reg := inflight.NewRegistry(nil, 0)
	e := reg.Track(inflight.Meta{ReqID: "x"})
	defer e.Done()
	res, err := HandleInflight(reg)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if !json.Valid(b) || !strings.Contains(string(b), `"x"`) {
		t.Fatalf("bad inflight payload: %s", b)
	}
}

func TestHandleGoroutines_NonEmpty(t *testing.T) {
	res, err := HandleGoroutines()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dump) == 0 {
		t.Fatal("expected non-empty goroutine dump")
	}
}
