package billing

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type lifecycleWorkerOwner struct {
	start  func()
	close  func(context.Context) error
	done   func() <-chan struct{}
	counts func() app.ResourceCounts
}

func TestBillingWorkersConcurrentStartCloseUseOneLifecycleGate(t *testing.T) {
	factories := map[string]func() lifecycleWorkerOwner{
		"aggregator": func() lifecycleWorkerOwner {
			owner := NewAggregator(nil, zap.NewNop(), AggregatorOptions{FlushEvery: time.Hour})
			return lifecycleWorkerOwner{
				start:  func() { owner.Start(context.Background()) },
				close:  owner.Close,
				done:   owner.Done,
				counts: owner.ResourceCounts,
			}
		},
		"limit evaluator": func() lifecycleWorkerOwner {
			owner := NewLimitEvaluator(nil, nil, zap.NewNop(), time.Hour)
			return lifecycleWorkerOwner{
				start:  owner.Start,
				close:  owner.Close,
				done:   owner.Done,
				counts: owner.ResourceCounts,
			}
		},
		"rebuild runner": func() lifecycleWorkerOwner {
			owner := NewRebuildRunner(nil, zap.NewNop(), time.Hour)
			return lifecycleWorkerOwner{
				start:  func() { owner.Start(context.Background()) },
				close:  owner.Close,
				done:   owner.Done,
				counts: owner.ResourceCounts,
			}
		},
	}

	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			started := factory()
			if got := started.counts(); got != (app.ResourceCounts{}) {
				t.Fatalf("resources before Start = %+v", got)
			}
			started.start()
			require.Eventually(t, func() bool {
				got := started.counts()
				return got.LifecycleWorkers == 1 && got.Timers == 1
			}, time.Second, time.Millisecond, "Start did not register one worker and timer")
			if err := started.close(context.Background()); err != nil {
				t.Fatalf("Close started owner: %v", err)
			}

			owner := factory()
			start := make(chan struct{})
			errs := make(chan error, 32)
			var callers conc.WaitGroup
			for i := 0; i < 64; i++ {
				if i%2 == 0 {
					callers.Go(func() {
						<-start
						owner.start()
					})
					continue
				}
				callers.Go(func() {
					<-start
					errs <- owner.close(context.Background())
				})
			}
			close(start)
			callers.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatalf("concurrent Close: %v", err)
				}
			}
			select {
			case <-owner.done():
			default:
				t.Fatal("Done remained open after concurrent Start/Close")
			}
			if got := owner.counts(); got != (app.ResourceCounts{}) {
				t.Fatalf("resources after concurrent Start/Close = %+v", got)
			}
			owner.start()
			if got := owner.counts(); got != (app.ResourceCounts{}) {
				t.Fatalf("Start after Close created resources: %+v", got)
			}
		})
	}
}
