package rpc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
)

func TestHandleInterrupt(t *testing.T) {
	reg := inflight.NewRegistry(nil, 0)
	_, cancel := context.WithCancel(context.Background())
	reg.Track(inflight.Meta{ReqID: "r1", Cancel: cancel})
	id := reg.Snapshot()[0].ID

	// hit
	out, err := HandleInterrupt(reg, mustJSON(map[string]any{"id": id}))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res, ok := out.(InterruptResult); !ok || !res.Interrupted {
		t.Fatalf("out = %+v, want Interrupted=true", out)
	}

	// miss
	out2, _ := HandleInterrupt(reg, mustJSON(map[string]any{"id": int64(999999)}))
	if res := out2.(InterruptResult); res.Interrupted {
		t.Fatalf("unknown id should not interrupt")
	}

	// nil reg
	out3, _ := HandleInterrupt(nil, mustJSON(map[string]any{"id": int64(1)}))
	if res := out3.(InterruptResult); res.Interrupted {
		t.Fatalf("nil reg should return Interrupted=false")
	}

	// bad params
	if _, err := HandleInterrupt(reg, json.RawMessage("{bad")); err == nil {
		t.Fatalf("invalid params should error")
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
