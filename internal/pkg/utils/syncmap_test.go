package utils

import (
	"sync"
	"testing"
)

func TestSyncMap_StoreAndLoad(t *testing.T) {
	var m SyncMap[string, int]
	m.Store("a", 1)
	m.Store("b", 2)

	v, ok := m.Load("a")
	if !ok || v != 1 {
		t.Fatalf("expected 1, got %d (ok=%v)", v, ok)
	}

	_, ok = m.Load("missing")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestSyncMap_Delete(t *testing.T) {
	var m SyncMap[string, int]
	m.Store("a", 1)
	m.Delete("a")
	_, ok := m.Load("a")
	if ok {
		t.Fatal("expected deleted")
	}
}

func TestSyncMap_Range(t *testing.T) {
	var m SyncMap[string, int]
	m.Store("a", 1)
	m.Store("b", 2)

	sum := 0
	m.Range(func(k string, v int) bool {
		sum += v
		return true
	})
	if sum != 3 {
		t.Fatalf("expected sum 3, got %d", sum)
	}
}

func TestSyncMap_Len(t *testing.T) {
	var m SyncMap[int, string]
	if m.Len() != 0 {
		t.Fatal("expected 0")
	}
	m.Store(1, "a")
	m.Store(2, "b")
	if m.Len() != 2 {
		t.Fatalf("expected 2, got %d", m.Len())
	}
}

func TestSyncMap_Keys(t *testing.T) {
	var m SyncMap[string, int]
	m.Store("x", 10)
	m.Store("y", 20)
	keys := m.Keys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}

func TestSyncMap_LoadOrStore(t *testing.T) {
	var m SyncMap[string, int]
	actual, loaded := m.LoadOrStore("a", 1)
	if loaded || actual != 1 {
		t.Fatalf("expected store: loaded=%v actual=%d", loaded, actual)
	}
	actual, loaded = m.LoadOrStore("a", 2)
	if !loaded || actual != 1 {
		t.Fatalf("expected load: loaded=%v actual=%d", loaded, actual)
	}
}

func TestSyncMap_LoadAndDelete(t *testing.T) {
	var m SyncMap[string, int]
	m.Store("a", 42)
	v, loaded := m.LoadAndDelete("a")
	if !loaded || v != 42 {
		t.Fatalf("expected 42, got %d (loaded=%v)", v, loaded)
	}
	_, loaded = m.Load("a")
	if loaded {
		t.Fatal("expected deleted")
	}
}

func TestSyncMap_ConcurrentAccess(t *testing.T) {
	var m SyncMap[int, int]
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.Store(i, i*2)
			m.Load(i)
			m.Delete(i)
		}(i)
	}
	wg.Wait()
}
