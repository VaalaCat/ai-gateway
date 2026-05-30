package inflight

import (
	"context"
	"testing"
)

func TestInterrupt_HitCallsCancel(t *testing.T) {
	reg := NewRegistry(nil, 0)
	ctx, cancel := context.WithCancel(context.Background())
	reg.Track(Meta{ReqID: "r1", Cancel: cancel})

	snaps := reg.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snaps))
	}
	id := snaps[0].ID

	if !reg.Interrupt(id) {
		t.Fatalf("Interrupt(%d) = false, want true", id)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("ctx.Err() = %v, want context.Canceled", ctx.Err())
	}
}

func TestInterrupt_MissReturnsFalse(t *testing.T) {
	reg := NewRegistry(nil, 0)
	if reg.Interrupt(999999) {
		t.Fatalf("Interrupt on unknown id = true, want false")
	}
}

func TestInterrupt_NilCancelReturnsFalse(t *testing.T) {
	reg := NewRegistry(nil, 0)
	reg.Track(Meta{ReqID: "r2"}) // no Cancel
	id := reg.Snapshot()[0].ID
	if reg.Interrupt(id) {
		t.Fatalf("Interrupt with nil cancel = true, want false")
	}
}
