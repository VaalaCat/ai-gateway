package inflight

import (
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestRegistry_TrackSnapshotDone(t *testing.T) {
	r := NewRegistry(nil, time.Hour)
	e := r.Track(Meta{ReqID: "r1", StartTime: time.Now()})
	e.Update(protocol.UsageLogEntry{ChannelID: 7, ModelName: "gpt-4o", IsStream: true})
	e.SetStage("upstream_dispatch")

	snaps := r.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("want 1 in-flight, got %d", len(snaps))
	}
	if snaps[0].ReqID != "r1" || snaps[0].View.ChannelID != 7 || snaps[0].Stage != "upstream_dispatch" {
		t.Fatalf("snapshot mismatch: %+v", snaps[0])
	}
	if snaps[0].ElapsedMs < 0 {
		t.Fatalf("elapsed should be >= 0, got %d", snaps[0].ElapsedMs)
	}

	e.Done()
	if got := len(r.Snapshot()); got != 0 {
		t.Fatalf("want 0 after Done, got %d", got)
	}
}

func TestRegistry_WatchdogWarnsStuck(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	r := NewRegistry(zap.New(core), 20*time.Millisecond)

	stop := r.StartWatchdog(10 * time.Millisecond)
	defer stop()

	e := r.Track(Meta{ReqID: "stuck", StartTime: time.Now().Add(-time.Second)}) // 已"卡" 1s
	defer e.Done()

	time.Sleep(60 * time.Millisecond)
	if logs.FilterMessageSnippet("in-flight relay request stuck").Len() == 0 {
		t.Fatalf("expected watchdog WARN for stuck request")
	}
}

func TestRegistry_WatchdogStopIsIdempotentAndConcurrentSafe(t *testing.T) {
	invokeStop := func(stop func()) (panicValue any) {
		defer func() { panicValue = recover() }()
		stop()
		return nil
	}

	t.Run("sequential", func(t *testing.T) {
		stop := NewRegistry(nil, time.Hour).StartWatchdog(time.Hour)
		if panicValue := invokeStop(stop); panicValue != nil {
			t.Fatalf("first stop panicked: %v", panicValue)
		}
		if panicValue := invokeStop(stop); panicValue != nil {
			t.Fatalf("second stop panicked: %v", panicValue)
		}
	})

	t.Run("concurrent", func(t *testing.T) {
		stop := NewRegistry(nil, time.Hour).StartWatchdog(time.Hour)
		start := make(chan struct{})
		results := make(chan any, 2)
		for range 2 {
			go func() {
				<-start
				results <- invokeStop(stop)
			}()
		}
		close(start)

		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		for range 2 {
			select {
			case panicValue := <-results:
				if panicValue != nil {
					t.Fatalf("concurrent stop panicked: %v", panicValue)
				}
			case <-deadline.C:
				t.Fatal("concurrent watchdog stops did not return")
			}
		}
	})
}

func TestEntry_UpdateAndQueuedSnapshot(t *testing.T) {
	r := NewRegistry(nil, 0)
	e := r.Track(Meta{ReqID: "r1", StartTime: time.Now().Add(-time.Second)})

	e.Update(protocol.UsageLogEntry{UserID: 7, ModelName: "gpt-4o", ChannelID: 42, InboundProtocol: "openai"})
	e.SetStage("ratelimit_wait")
	e.MarkQueued("free-tier(concurrency/per_user) over capacity 1")
	time.Sleep(2 * time.Millisecond) // ensure at least 1ms elapses so QueuedMs > 0

	snaps := r.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("want 1 snap, got %d", len(snaps))
	}
	s := snaps[0]
	if s.View.UserID != 7 || s.View.ModelName != "gpt-4o" || s.View.ChannelID != 42 {
		t.Fatalf("view not carried: %+v", s.View)
	}
	if s.Stage != "ratelimit_wait" || s.QueuedMs <= 0 || s.QueuedReason == "" {
		t.Fatalf("queue marker wrong: stage=%s queuedMs=%d reason=%q", s.Stage, s.QueuedMs, s.QueuedReason)
	}

	e.Unqueue()
	if r.Snapshot()[0].QueuedMs != 0 {
		t.Fatal("Unqueue should zero QueuedMs")
	}
}
