package entitycache

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/sourcegraph/conc"
)

type Lifecycle struct {
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelCauseFunc
	closing   bool
	closeOnce sync.Once
	workers   conc.WaitGroup
	done      chan struct{}
	loads     atomic.Int64
	refreshes atomic.Int64
}

func NewLifecycle() *Lifecycle {
	ctx, cancel := context.WithCancelCause(context.Background())
	return &Lifecycle{ctx: ctx, cancel: cancel, done: make(chan struct{})}
}

func (l *Lifecycle) Context() context.Context { return l.ctx }

func (l *Lifecycle) Go(run func(context.Context)) bool {
	return l.goCounted(nil, run)
}

func (l *Lifecycle) GoLoad(run func(context.Context)) bool {
	return l.goCounted(&l.loads, run)
}

func (l *Lifecycle) GoRefresh(run func(context.Context)) bool {
	return l.goCounted(&l.refreshes, run)
}

func (l *Lifecycle) goCounted(counter *atomic.Int64, run func(context.Context)) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		return false
	}
	if counter != nil {
		counter.Add(1)
	}
	l.workers.Go(func() {
		if counter != nil {
			defer counter.Add(-1)
		}
		run(l.ctx)
	})
	return true
}

func (l *Lifecycle) Close() {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closing = true
		cancel := l.cancel
		l.mu.Unlock()
		cancel(context.Canceled)
		l.workers.Wait()
		close(l.done)
	})
}

func (l *Lifecycle) Done() <-chan struct{} { return l.done }

func (l *Lifecycle) ResourceCounts() (loads, refreshes int64) {
	return l.loads.Load(), l.refreshes.Load()
}
