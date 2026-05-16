package cache

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestRouteIndex_Match_TokenModel(t *testing.T) {
	ri := NewRouteIndex()
	ri.Put(&models.AgentRoute{ID: 1, SourceType: "token", SourceID: 10, Model: "gpt-4o", AgentTag: "gpu", Priority: 100})
	ri.Put(&models.AgentRoute{ID: 2, SourceType: "token", SourceID: 10, Model: "", AgentID: "agent-default", Priority: 90})

	route := ri.Match(10, "gpt-4o", nil)
	if route == nil || route.ID != 1 {
		t.Fatalf("expected route 1, got %v", route)
	}
}

func TestRouteIndex_Match_TokenDefault(t *testing.T) {
	ri := NewRouteIndex()
	ri.Put(&models.AgentRoute{ID: 2, SourceType: "token", SourceID: 10, Model: "", AgentID: "agent-default", Priority: 90})

	route := ri.Match(10, "gpt-4o", nil)
	if route == nil || route.ID != 2 {
		t.Fatalf("expected route 2, got %v", route)
	}
}

func TestRouteIndex_Match_ChannelModel(t *testing.T) {
	ri := NewRouteIndex()
	ri.Put(&models.AgentRoute{ID: 3, SourceType: "channel", SourceID: 20, Model: "gpt-4o", AgentTag: "gpu", Priority: 80})

	route := ri.Match(10, "gpt-4o", []uint{20, 30})
	if route == nil || route.ID != 3 {
		t.Fatalf("expected route 3, got %v", route)
	}
}

func TestRouteIndex_Match_ChannelDefault(t *testing.T) {
	ri := NewRouteIndex()
	ri.Put(&models.AgentRoute{ID: 4, SourceType: "channel", SourceID: 20, Model: "", AgentID: "agent-ch", Priority: 70})

	route := ri.Match(10, "gpt-4o", []uint{20})
	if route == nil || route.ID != 4 {
		t.Fatalf("expected route 4, got %v", route)
	}
}

func TestRouteIndex_Match_Priority(t *testing.T) {
	ri := NewRouteIndex()
	ri.Put(&models.AgentRoute{ID: 1, SourceType: "token", SourceID: 10, Model: "gpt-4o", AgentTag: "gpu", Priority: 100})
	ri.Put(&models.AgentRoute{ID: 3, SourceType: "channel", SourceID: 20, Model: "gpt-4o", AgentTag: "cpu", Priority: 80})

	route := ri.Match(10, "gpt-4o", []uint{20})
	if route == nil || route.ID != 1 {
		t.Fatalf("expected token-model route (ID=1), got %v", route)
	}
}

func TestRouteIndex_Match_NoMatch(t *testing.T) {
	ri := NewRouteIndex()
	ri.Put(&models.AgentRoute{ID: 1, SourceType: "token", SourceID: 99, Model: "gpt-4o", AgentTag: "gpu", Priority: 100})

	route := ri.Match(10, "gpt-4o", []uint{20})
	if route != nil {
		t.Fatalf("expected nil, got %v", route)
	}
}

func TestRouteIndex_Delete(t *testing.T) {
	ri := NewRouteIndex()
	ri.Put(&models.AgentRoute{ID: 1, SourceType: "token", SourceID: 10, Model: "gpt-4o", AgentTag: "gpu", Priority: 100})
	ri.Delete(1)

	route := ri.Match(10, "gpt-4o", nil)
	if route != nil {
		t.Fatalf("expected nil after delete, got %v", route)
	}
}

func TestRouteIndex_Load(t *testing.T) {
	ri := NewRouteIndex()
	ri.Put(&models.AgentRoute{ID: 99, SourceType: "token", SourceID: 1, Model: "", AgentTag: "old", Priority: 90})

	// Load should clear old data and rebuild
	ri.Load([]*models.AgentRoute{
		{ID: 1, SourceType: "token", SourceID: 10, Model: "gpt-4o", AgentTag: "gpu", Priority: 100},
	})

	// Old data should be gone
	route := ri.Match(1, "anything", nil)
	if route != nil {
		t.Fatalf("expected old route gone, got %v", route)
	}

	// New data should exist
	route = ri.Match(10, "gpt-4o", nil)
	if route == nil || route.ID != 1 {
		t.Fatalf("expected route 1, got %v", route)
	}
}
