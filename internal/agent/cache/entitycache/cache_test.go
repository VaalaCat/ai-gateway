package entitycache

import (
	"context"
	"errors"
	"testing"
)

func TestActionConstants(t *testing.T) {
	if ActionSet == ActionDelete {
		t.Fatal("ActionSet must differ from ActionDelete")
	}
}

func TestErrNotFoundIs(t *testing.T) {
	wrapped := errors.Join(errors.New("ctx"), ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Fatal("ErrNotFound should be matchable via errors.Is")
	}
}

func TestStatsZero(t *testing.T) {
	var s Stats
	if s.Hits != 0 || s.Misses != 0 || s.Evictions != 0 || s.NegativeHits != 0 {
		t.Fatal("zero-value Stats fields should all be 0")
	}
}

func TestFullCache_SetGetDelete(t *testing.T) {
	c := NewFullCache[string, int]()

	v, ok := c.Peek("a")
	if ok || v != 0 {
		t.Fatalf("Peek miss: got (%d, %v)", v, ok)
	}

	c.Set("a", 1)
	v, ok = c.Peek("a")
	if !ok || v != 1 {
		t.Fatalf("Peek hit: got (%d, %v)", v, ok)
	}

	c.Delete("a")
	if _, ok := c.Peek("a"); ok {
		t.Fatal("Peek after delete should miss")
	}
}

func TestFullCache_GetWithoutLoader(t *testing.T) {
	c := NewFullCache[string, int]()
	v, ok, err := c.Get(context.Background(), "missing")
	if ok || v != 0 || err != nil {
		t.Fatalf("FullCache.Get on missing key should be (0, false, nil), got (%d, %v, %v)", v, ok, err)
	}
	c.Set("a", 5)
	v, ok, err = c.Get(context.Background(), "a")
	if !ok || v != 5 || err != nil {
		t.Fatalf("FullCache.Get hit: got (%d, %v, %v)", v, ok, err)
	}
}

func TestFullCache_ApplyAlwaysWrites(t *testing.T) {
	c := NewFullCache[string, int]()

	c.Apply(ActionSet, "a", 1)
	if v, ok := c.Peek("a"); !ok || v != 1 {
		t.Fatalf("Apply(Set) should always write, got (%d, %v)", v, ok)
	}

	c.Apply(ActionDelete, "a", 0)
	if _, ok := c.Peek("a"); ok {
		t.Fatal("Apply(Delete) should always delete")
	}
}

func TestFullCache_RangeAndLen(t *testing.T) {
	c := NewFullCache[string, int]()
	c.Set("a", 1)
	c.Set("b", 2)
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
	count := 0
	c.Range(func(_ string, _ int) bool {
		count++
		return true
	})
	if count != 2 {
		t.Fatalf("Range visited %d items, want 2", count)
	}
}

func TestFullCache_Stats(t *testing.T) {
	c := NewFullCache[string, int]()
	c.Set("a", 1)
	s := c.Stats()
	if s.Size != 1 {
		t.Fatalf("Stats.Size = %d, want 1", s.Size)
	}
	// FullCache 不维护 hits/misses/evictions/negativeHits
	if s.Hits != 0 || s.Misses != 0 || s.Evictions != 0 || s.NegativeHits != 0 {
		t.Fatal("FullCache should not maintain hit/miss counters")
	}
}
