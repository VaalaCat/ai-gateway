package affinity

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

func TestTTLStore_RememberLookup(t *testing.T) {
	s := newTTLStore()
	k := Key{UserID: 1, RealModel: "claude-3-5-sonnet"}
	s.Remember(k, Entry{Source: state.SourceAdmin, SourceID: 7, ExpiresAt: time.Now().Add(time.Minute)})
	e, ok := s.Lookup(k)
	if !ok || e.Source != state.SourceAdmin || e.SourceID != 7 {
		t.Fatalf("lookup = (%+v,%v), want admin/7/true", e, ok)
	}
}

func TestTTLStore_Expired(t *testing.T) {
	s := newTTLStore()
	k := Key{UserID: 1, RealModel: "m"}
	s.Remember(k, Entry{Source: state.SourceAdmin, SourceID: 7, ExpiresAt: time.Now().Add(-time.Second)})
	if _, ok := s.Lookup(k); ok {
		t.Fatal("expired entry should miss")
	}
	if s.m.Len() != 0 {
		t.Fatal("expired entry should be lazily deleted on lookup")
	}
}

func TestTTLStore_Forget(t *testing.T) {
	s := newTTLStore()
	k := Key{UserID: 1, RealModel: "m"}
	s.Remember(k, Entry{Source: state.SourcePrivate, SourceID: 3, ExpiresAt: time.Now().Add(time.Minute)})
	s.Forget(k)
	if _, ok := s.Lookup(k); ok {
		t.Fatal("forgotten entry should miss")
	}
}
