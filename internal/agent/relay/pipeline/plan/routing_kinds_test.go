package plan

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"testing"
)

func TestResolve_TypedSameNameAlternativesRemainDistinct(t *testing.T) {
	s := newTestStore()
	t.Cleanup(s.Close)
	s.SetGlobalRouting("outer", &protocol.SyncedRouting{ID: 1, Name: "outer", Enabled: true, Members: []protocol.RoutingMember{
		{Kind: "model", Ref: "same", Priority: 10, Weight: 1},
		{Kind: "routing", Ref: "same", Priority: 0, Weight: 1},
	}})
	s.SetGlobalRouting("same", &protocol.SyncedRouting{ID: 2, Name: "same", Enabled: true, Members: []protocol.RoutingMember{{Kind: "model", Ref: "other", Weight: 1}}})
	walk := NewResolveCtx()
	if got := resolveToRealModel(s, "outer", 0, walk); got != "same" {
		t.Fatalf("explicit model must not expand: %q", got)
	}
	walk.MarkMemberExhausted("same")
	if got := resolveToRealModel(s, "outer", 0, walk); got != "other" {
		t.Fatalf("same-name routing is a separate retry target: %q", got)
	}
}

func TestResolve_TypedRoutingNeverFallsBackToSameNameModel(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		nested     bool
	}{
		{"missing routing", "routing", false}, {"unknown kind", "auto", false}, {"missing kind", "", false}, {"indirect cycle", "routing", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore()
			t.Cleanup(s.Close)
			s.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: 1}, Models: "A,B"})
			s.RebuildModelIndex()
			s.SetGlobalRouting("A", &protocol.SyncedRouting{ID: 1, Name: "A", Enabled: true, Members: []protocol.RoutingMember{{Kind: tc.kind, Ref: "B", Weight: 1}}})
			if tc.nested {
				s.SetGlobalRouting("B", &protocol.SyncedRouting{ID: 2, Name: "B", Enabled: true, Members: []protocol.RoutingMember{{Kind: "routing", Ref: "A", Weight: 1}}})
			}
			if got := resolveToRealModel(s, "A", 0, NewResolveCtx()); got != "" {
				t.Fatalf("invalid branch must fail closed: %q", got)
			}
		})
	}
}
