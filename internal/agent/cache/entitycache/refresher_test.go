package entitycache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func fastCfg() RefreshConfig {
	return RefreshConfig{
		RefreshTimeout:     time.Second,
		RefreshMaxRetries:  3,
		RefreshBackoffBase: time.Millisecond,
		RefreshBackoffMax:  2 * time.Millisecond,
	}
}

func TestRefresherDedupesConcurrent(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int64
	loader := LoaderFunc[string, string](func(_ context.Context, _ string) (string, error) {
		calls.Add(1)
		<-release // 卡住第一次,制造"在飞"窗口
		return "v", nil
	})
	r := NewRefresher[string, string](loader, fastCfg)

	got := make(chan RefreshOutcome, 5)
	for i := 0; i < 5; i++ {
		r.TriggerRefresh("k", func(o RefreshOutcome, _ string) { got <- o })
	}
	time.Sleep(50 * time.Millisecond) // 让首个 goroutine 跑到 Load
	close(release)

	select {
	case o := <-got:
		if o != RefreshOK {
			t.Fatalf("got %v, want RefreshOK", o)
		}
	case <-time.After(time.Second):
		t.Fatal("no callback")
	}
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("Load called %d times, want 1 (deduped)", n)
	}
	select {
	case <-got:
		t.Fatal("unexpected second callback")
	default:
	}
}

func TestRefresherRetriesUntilSuccess(t *testing.T) {
	var calls atomic.Int64
	loader := LoaderFunc[string, string](func(_ context.Context, _ string) (string, error) {
		if calls.Add(1) < 3 {
			return "", errors.New("master unavailable")
		}
		return "recovered", nil
	})
	r := NewRefresher[string, string](loader, fastCfg)

	got := make(chan string, 1)
	r.TriggerRefresh("k", func(o RefreshOutcome, v string) {
		if o == RefreshOK {
			got <- v
		}
	})
	select {
	case v := <-got:
		if v != "recovered" {
			t.Fatalf("got %q, want recovered", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("never recovered")
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("Load called %d times, want 3", n)
	}
}

func TestRefresherNotFoundIsGoneNoRetry(t *testing.T) {
	var calls atomic.Int64
	loader := LoaderFunc[string, string](func(_ context.Context, _ string) (string, error) {
		calls.Add(1)
		return "", ErrNotFound
	})
	r := NewRefresher[string, string](loader, fastCfg)

	got := make(chan RefreshOutcome, 1)
	r.TriggerRefresh("k", func(o RefreshOutcome, _ string) { got <- o })
	select {
	case o := <-got:
		if o != RefreshGone {
			t.Fatalf("got %v, want RefreshGone", o)
		}
	case <-time.After(time.Second):
		t.Fatal("no callback")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("Load called %d times, want 1 (NotFound must not retry)", n)
	}
}
