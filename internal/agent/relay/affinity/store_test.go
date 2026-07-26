package affinity

import (
	"reflect"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

func TestKeyIncludesTokenID(t *testing.T) {
	field, ok := reflect.TypeOf(Key{}).FieldByName("TokenID")
	if !ok {
		t.Fatal("affinity key must include TokenID")
	}
	if field.Type.Kind() != reflect.Uint {
		t.Fatalf("TokenID kind = %s, want uint", field.Type.Kind())
	}
}

func TestTTLStore_RememberLookup(t *testing.T) {
	s := newTTLStore()
	k := Key{UserID: 1, RealModel: "claude-3-5-sonnet"}
	s.Remember(k, Entry{Source: state.SourceAdmin, SourceID: 7, ExpiresAt: time.Now().Add(time.Minute)})
	e, ok := s.Lookup(k)
	if !ok || e.Source != state.SourceAdmin || e.SourceID != 7 {
		t.Fatalf("lookup = (%+v,%v), want admin/7/true", e, ok)
	}
}

func TestTTLStore_IsolatesEntriesByTokenID(t *testing.T) {
	s := newTTLStore()
	expiresAt := time.Now().Add(time.Minute)
	entries := []struct {
		tokenID  uint
		sourceID uint
	}{
		{tokenID: 0, sourceID: 100},
		{tokenID: 11, sourceID: 111},
		{tokenID: 22, sourceID: 122},
	}

	for _, entry := range entries {
		key := Key{UserID: 1, TokenID: entry.tokenID, RealModel: "m"}
		s.Remember(key, Entry{Source: state.SourceAdmin, SourceID: entry.sourceID, ExpiresAt: expiresAt})
	}
	for _, want := range entries {
		key := Key{UserID: 1, TokenID: want.tokenID, RealModel: "m"}
		got, ok := s.Lookup(key)
		if !ok || got.SourceID != want.sourceID {
			t.Fatalf("token %d lookup = (%+v, %v), want source %d", want.tokenID, got, ok, want.sourceID)
		}
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
