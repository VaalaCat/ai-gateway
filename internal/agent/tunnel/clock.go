package tunnel

import (
	"context"
	"sync/atomic"
	"time"
)

func withClockTimeoutCause(parent context.Context, clock sessionClock, duration time.Duration, cause error) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	timer := clock.NewTimer(duration)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.Chan():
			cancel(cause)
		}
	}()
	return ctx, func() {
		cancel(context.Canceled)
		timer.Stop()
		<-done
	}
}

type sessionClock interface {
	Now() time.Time
	NewTimer(time.Duration) sessionTimer
	NewTicker(time.Duration) sessionTicker
}

type sessionTimer interface {
	Chan() <-chan time.Time
	Stop() bool
}

type sessionTicker interface {
	Chan() <-chan time.Time
	Stop() bool
}

type realSessionClock struct {
	now func() time.Time
}

func (c realSessionClock) Now() time.Time { return c.now() }

func (c realSessionClock) NewTimer(duration time.Duration) sessionTimer {
	return realSessionTimer{Timer: time.NewTimer(duration)}
}

func (c realSessionClock) NewTicker(duration time.Duration) sessionTicker {
	return &realSessionTicker{ticker: time.NewTicker(duration)}
}

type realSessionTimer struct{ *time.Timer }

func (t realSessionTimer) Chan() <-chan time.Time { return t.C }

type realSessionTicker struct {
	ticker  *time.Ticker
	stopped atomic.Bool
}

func (t *realSessionTicker) Chan() <-chan time.Time { return t.ticker.C }

func (t *realSessionTicker) Stop() bool {
	if !t.stopped.CompareAndSwap(false, true) {
		return false
	}
	t.ticker.Stop()
	return true
}
