package entitycache

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func swrCfg() func() RefreshConfig {
	return func() RefreshConfig {
		return RefreshConfig{
			LoadTimeout:        time.Second,
			RefreshAfter:       10 * time.Millisecond,
			RefreshTimeout:     time.Second,
			RefreshMaxRetries:  2,
			RefreshBackoffBase: time.Millisecond,
			RefreshBackoffMax:  2 * time.Millisecond,
		}
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestLRUServeStaleAndRefresh(t *testing.T) {
	var calls atomic.Int64
	loader := LoaderFunc[string, string](func(_ context.Context, _ string) (string, error) {
		return fmt.Sprintf("v%d", calls.Add(1)), nil
	})
	c, _ := NewLRUCache[string, string](Config[string, string]{
		Capacity: 4, Loader: loader, Refresh: swrCfg(),
	})

	v, _, _ := c.Get(context.Background(), "k") // 冷 miss → v1
	if v != "v1" {
		t.Fatalf("cold load got %q, want v1", v)
	}
	time.Sleep(20 * time.Millisecond) // 超过 RefreshAfter

	v, _, _ = c.Get(context.Background(), "k") // 立即返回旧值并触发后台刷新
	if v != "v1" {
		t.Fatalf("stale get got %q, want immediate v1", v)
	}
	waitUntil(t, time.Second, func() bool {
		got, _ := c.Peek("k")
		return got == "v2" // 后台刷新写回
	})
}

func TestLRURefreshGoneEvicts(t *testing.T) {
	var notFound atomic.Bool
	loader := LoaderFunc[string, string](func(_ context.Context, _ string) (string, error) {
		if notFound.Load() {
			return "", ErrNotFound
		}
		return "v1", nil
	})
	c, _ := NewLRUCache[string, string](Config[string, string]{
		Capacity: 4, Loader: loader, Refresh: swrCfg(),
	})

	c.Get(context.Background(), "k") // v1 cached
	notFound.Store(true)
	time.Sleep(20 * time.Millisecond)
	c.Get(context.Background(), "k") // serve-stale + 触发刷新 → NotFound → Delete

	waitUntil(t, time.Second, func() bool {
		_, ok := c.Peek("k")
		return !ok // 被逐出(revocation)
	})
}

func TestLRURefreshUnavailableKeepsStale(t *testing.T) {
	var down atomic.Bool
	var calls atomic.Int64
	loader := LoaderFunc[string, string](func(_ context.Context, _ string) (string, error) {
		calls.Add(1)
		if down.Load() {
			return "", errors.New("master down")
		}
		return "v1", nil
	})
	c, _ := NewLRUCache[string, string](Config[string, string]{
		Capacity: 4, Loader: loader, Refresh: swrCfg(),
	})

	c.Get(context.Background(), "k") // 冷 miss → v1, calls=1
	down.Store(true)
	time.Sleep(20 * time.Millisecond) // 超过 RefreshAfter
	c.Get(context.Background(), "k")  // 触发后台刷新(将失败)
	// 等后台刷新真的跑过(至少一次失败尝试),而不是固定 sleep
	waitUntil(t, time.Second, func() bool { return calls.Load() >= 2 })

	got, ok := c.Peek("k")
	if !ok || got != "v1" {
		t.Fatalf("stale evicted on unavailable: ok=%v got=%q (want ok=true v1)", ok, got)
	}
}
