package oauth

import (
	"sync"
	"testing"
	"time"
)

func TestStateStore_PutTake(t *testing.T) {
	s := NewStateStore()
	e := &StateEntry{ProviderID: 1, Kind: "login", ReturnTo: "/", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	s.Put("st1", e)
	got, ok := s.Take("st1")
	if !ok || got.ProviderID != 1 {
		t.Fatalf("first Take should return: %v %v", got, ok)
	}
	if _, ok := s.Take("st1"); ok {
		t.Fatal("second Take should miss (single-use)")
	}
}

func TestStateStore_Expired(t *testing.T) {
	s := NewStateStore()
	s.Put("st1", &StateEntry{ExpiresAt: time.Now().Add(-time.Second).Unix()})
	if _, ok := s.Take("st1"); ok {
		t.Fatal("expired Take should miss")
	}
}

func TestStateStore_Sweep(t *testing.T) {
	s := NewStateStore()
	s.Put("expired", &StateEntry{ExpiresAt: time.Now().Add(-time.Second).Unix()})
	s.Put("alive", &StateEntry{ExpiresAt: time.Now().Add(time.Minute).Unix()})
	s.Sweep()
	if _, ok := s.Take("alive"); !ok {
		t.Fatal("alive should still exist")
	}
	if _, ok := s.Take("expired"); ok {
		t.Fatal("expired should have been swept")
	}
}

func TestStateStore_Concurrent(t *testing.T) {
	s := NewStateStore()
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := string(rune('a' + (i % 26)))
			s.Put(key, &StateEntry{ProviderID: uint(i), ExpiresAt: time.Now().Add(time.Minute).Unix()})
			s.Take(key)
		}(i)
	}
	wg.Wait()
}
