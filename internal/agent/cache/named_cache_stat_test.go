package cache

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestRouteIndex_CacheStat(t *testing.T) {
	ri := NewRouteIndex()
	ri.Load([]*models.AgentRoute{{ID: 1}, {ID: 2}})
	var ncs NamedCacheStat = ri
	if ncs.CacheName() != "route_index" {
		t.Fatalf("name=%s", ncs.CacheName())
	}
	st := ncs.CacheStat()
	if st.Kind != "index" || st.Size != 2 {
		t.Fatalf("kind=%s size=%d", st.Kind, st.Size)
	}
}

func TestLimiterIndex_CacheStat(t *testing.T) {
	li := NewLimiterIndex()
	li.LoadLimiters([]models.RequestLimiter{{ID: 1, Enabled: true}})
	li.LoadBindings([]models.LimiterBinding{{ID: 1, LimiterID: 1, TargetType: "global", Enabled: true}})
	var ncs NamedCacheStat = li
	if ncs.CacheName() != "limiter_index" {
		t.Fatalf("name=%s", ncs.CacheName())
	}
	st := ncs.CacheStat()
	if st.Kind != "index" || st.Extra["limiters"] != 1 || st.Extra["bindings"] != 1 {
		t.Fatalf("stat=%+v", st)
	}
}
