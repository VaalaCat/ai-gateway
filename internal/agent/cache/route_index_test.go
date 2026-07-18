package cache

import (
	"reflect"
	"runtime"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/stretchr/testify/require"
)

func TestRouteIndex_SortRouteIDsByPriority_TotalOrder(t *testing.T) {
	routes := map[uint]models.AgentRoute{
		1: {ID: 1, Priority: 100},
		2: {ID: 2, Priority: 100},
		3: {ID: 3, Priority: 100},
	}
	routeIDs := []uint{3, 2, 1}

	sortRouteIDsByPriority(routeIDs, routes)

	require.Equal(t, []uint{1, 2, 3}, routeIDs)
}

func TestRouteIndex_ApplyActions(t *testing.T) {
	t.Run("nil route is ignored", func(t *testing.T) {
		idx := NewRouteIndex()
		idx.Replace([]*models.AgentRoute{{
			ID: 1, SourceType: "token", SourceID: 42,
			Model: "model", AgentID: "kept", Priority: 100,
		}})
		before := idx.state.Load()

		for _, action := range []string{events.ActionCreate, events.ActionUpdate, events.ActionDelete, "unknown"} {
			require.NotPanics(t, func() {
				idx.Apply(action, nil)
			})
		}
		require.Same(t, before, idx.state.Load())
	})

	t.Run("unknown action is ignored", func(t *testing.T) {
		idx := NewRouteIndex()
		idx.Replace([]*models.AgentRoute{{
			ID: 1, SourceType: "token", SourceID: 42,
			Model: "model", AgentID: "kept", Priority: 100,
		}})
		before := idx.state.Load()

		idx.Apply("unknown", &models.AgentRoute{
			ID: 2, SourceType: "token", SourceID: 99,
			Model: "model", AgentID: "unexpected", Priority: 100,
		})

		require.Same(t, before, idx.state.Load())
		require.Nil(t, idx.FindTokenRoute(99, "model"))
	})

	t.Run("known actions mutate", func(t *testing.T) {
		idx := NewRouteIndex()
		created := &models.AgentRoute{
			ID: 1, SourceType: "token", SourceID: 42,
			Model: "model", AgentID: "created", Priority: 100,
		}
		idx.Apply(events.ActionCreate, created)
		matched := idx.FindTokenRoute(42, "model")
		require.NotNil(t, matched)
		require.Equal(t, "created", matched.AgentID)

		updated := *created
		updated.AgentID = "updated"
		idx.Apply(events.ActionUpdate, &updated)
		matched = idx.FindTokenRoute(42, "model")
		require.NotNil(t, matched)
		require.Equal(t, "updated", matched.AgentID)

		idx.Apply(events.ActionDelete, &updated)
		require.Nil(t, idx.FindTokenRoute(42, "model"))
	})
}

func TestRouteIndex_NilInputsAreIgnored(t *testing.T) {
	t.Run("replace skips nil routes", func(t *testing.T) {
		idx := NewRouteIndex()
		route := &models.AgentRoute{
			ID: 1, SourceType: "token", SourceID: 42,
			Model: "model", AgentID: "kept", Priority: 100,
		}

		require.NotPanics(t, func() {
			idx.Replace([]*models.AgentRoute{nil, route, nil})
		})
		matched := idx.FindTokenRoute(42, "model")
		require.NotNil(t, matched)
		require.Equal(t, "kept", matched.AgentID)
	})

	t.Run("put ignores nil route", func(t *testing.T) {
		idx := NewRouteIndex()
		idx.Replace([]*models.AgentRoute{{
			ID: 1, SourceType: "token", SourceID: 42,
			Model: "model", AgentID: "kept", Priority: 100,
		}})
		before := idx.state.Load()

		require.NotPanics(t, func() {
			idx.Put(nil)
		})
		require.Same(t, before, idx.state.Load())
	})
}

func TestRouteIndex_ApplyConcurrentWithFind(t *testing.T) {
	idx := NewRouteIndex()
	first := &models.AgentRoute{
		ID: 1, SourceType: "token", SourceID: 42,
		Model: "model", AgentID: "first", Priority: 100,
	}
	second := *first
	second.AgentID = "second"
	idx.Apply(events.ActionCreate, first)

	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	bad := make(chan *models.AgentRoute, 1)
	go func() {
		defer close(done)
		firstRead := true
		for {
			route := idx.FindTokenRoute(42, "model")
			if firstRead {
				close(started)
				firstRead = false
			}
			if route == nil || (route.AgentID != "first" && route.AgentID != "second") {
				bad <- route
				return
			}

			select {
			case <-stop:
				return
			default:
				runtime.Gosched()
			}
		}
	}()

	<-started
	for i := 0; i < 1_000; i++ {
		idx.Apply(events.ActionUpdate, &second)
		runtime.Gosched()
		idx.Apply(events.ActionUpdate, first)
		runtime.Gosched()
	}
	close(stop)
	<-done
	select {
	case route := <-bad:
		t.Fatalf("observed invalid route during concurrent Apply: %+v", route)
	default:
	}
}

func TestRouteIndex_ReplaceIsAtomic(t *testing.T) {
	idx := NewRouteIndex()
	old := []*models.AgentRoute{
		{ID: 1, SourceType: "token", SourceID: 42, Model: "model", AgentID: "old-model", Priority: 100},
		{ID: 2, SourceType: "token", SourceID: 42, AgentID: "old-default", Priority: 90},
		{ID: 3, SourceType: "channel", SourceID: 7, Model: "model", AgentID: "old-channel-model", Priority: 80},
		{ID: 4, SourceType: "channel", SourceID: 7, AgentID: "old-channel-default", Priority: 70},
	}
	next := []*models.AgentRoute{
		{ID: 11, SourceType: "token", SourceID: 42, Model: "model", AgentID: "new-model", Priority: 100},
		{ID: 12, SourceType: "token", SourceID: 42, AgentID: "new-default", Priority: 90},
		{ID: 13, SourceType: "channel", SourceID: 7, Model: "model", AgentID: "new-channel-model", Priority: 80},
		{ID: 14, SourceType: "channel", SourceID: 7, AgentID: "new-channel-default", Priority: 70},
	}
	oldState := &routeIndexState{
		routes: map[uint]models.AgentRoute{
			1: *old[0], 2: *old[1], 3: *old[2], 4: *old[3],
		},
		byToken:   map[uint][]uint{42: {1, 2}},
		byChannel: map[uint][]uint{7: {3, 4}},
	}
	nextState := &routeIndexState{
		routes: map[uint]models.AgentRoute{
			11: *next[0], 12: *next[1], 13: *next[2], 14: *next[3],
		},
		byToken:   map[uint][]uint{42: {11, 12}},
		byChannel: map[uint][]uint{7: {13, 14}},
	}
	idx.Replace(old)

	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	bad := make(chan struct{}, 1)
	go func() {
		defer close(done)
		first := true
		for {
			state := idx.state.Load()
			valid := routeIndexStatesEqual(state, oldState) || routeIndexStatesEqual(state, nextState)
			if first {
				close(started)
				first = false
			}
			if !valid {
				bad <- struct{}{}
				return
			}

			select {
			case <-stop:
				return
			default:
				runtime.Gosched()
			}
		}
	}()
	<-started
	for i := 0; i < 1_000; i++ {
		idx.Replace(next)
		runtime.Gosched()
		idx.Replace(old)
		runtime.Gosched()
	}
	close(stop)
	<-done
	select {
	case <-bad:
		t.Fatal("observed a route index snapshot mixing old and new generations")
	default:
	}
}

func TestRouteIndex_PutDeleteCopyOnWrite(t *testing.T) {
	idx := NewRouteIndex()
	old := models.AgentRoute{ID: 1, SourceType: "token", SourceID: 42, Model: "model", AgentID: "old", Priority: 100}
	kept := models.AgentRoute{ID: 2, SourceType: "channel", SourceID: 7, Model: "model", AgentID: "kept", Priority: 80}
	idx.Replace([]*models.AgentRoute{&old, &kept})
	beforePut := idx.state.Load()

	updated := &models.AgentRoute{
		ID: 1, SourceType: "channel", SourceID: 7,
		Model: "model", AgentID: "updated", Priority: 100,
	}
	wantUpdated := *updated
	idx.Put(updated)
	afterPut := idx.state.Load()
	require.NotSame(t, beforePut, afterPut)

	updated.AgentID = "mutated-after-put"
	updated.SourceID = 99
	matched := idx.FindAdminChannelRoute(7, "model")
	require.NotNil(t, matched)
	require.Equal(t, "updated", matched.AgentID)
	require.Equal(t, uint(7), matched.SourceID)

	beforeDelete := idx.state.Load()
	require.Same(t, afterPut, beforeDelete)
	idx.Delete(1)
	afterDelete := idx.state.Load()
	require.NotSame(t, beforeDelete, afterDelete)

	requireRouteIndexState(t, beforePut,
		map[uint]models.AgentRoute{1: old, 2: kept},
		map[uint][]uint{42: {1}},
		map[uint][]uint{7: {2}},
	)
	requireRouteIndexState(t, beforeDelete,
		map[uint]models.AgentRoute{1: wantUpdated, 2: kept},
		map[uint][]uint{},
		map[uint][]uint{7: {1, 2}},
	)
	requireRouteIndexState(t, afterDelete,
		map[uint]models.AgentRoute{2: kept},
		map[uint][]uint{},
		map[uint][]uint{7: {2}},
	)

	require.Nil(t, idx.FindTokenRoute(42, "model"))
	matched = idx.FindAdminChannelRoute(7, "model")
	require.NotNil(t, matched)
	require.Equal(t, "kept", matched.AgentID)
}

func TestRouteIndex_ReturnedRouteCannotMutateSnapshot(t *testing.T) {
	idx := NewRouteIndex()
	input := &models.AgentRoute{
		ID: 1, SourceType: "token", SourceID: 42,
		Model: "model", AgentID: "original", Priority: 100,
	}
	idx.Replace([]*models.AgentRoute{input})
	input.AgentID = "mutated-input"
	input.Model = "mutated-input-model"

	matched := idx.FindTokenRoute(42, "model")
	require.NotNil(t, matched)
	require.Equal(t, "original", matched.AgentID)
	require.Equal(t, "model", matched.Model)
	matched.AgentID = "mutated"
	matched.Model = "other-model"

	again := idx.FindTokenRoute(42, "model")
	require.NotNil(t, again)
	require.Equal(t, "original", again.AgentID)
	require.Equal(t, "model", again.Model)
}

func TestRouteIndexFindTokenRoute(t *testing.T) {
	tests := []struct {
		name   string
		routes []*models.AgentRoute
		model  string
		wantID uint
	}{
		{
			name: "exact beats earlier default",
			routes: []*models.AgentRoute{
				{ID: 1, SourceType: "token", SourceID: 9, AgentID: "default", Priority: 100},
				{ID: 2, SourceType: "token", SourceID: 9, Model: "gpt-4o", AgentID: "exact", Priority: 1},
			},
			model:  "gpt-4o",
			wantID: 2,
		},
		{
			name: "default",
			routes: []*models.AgentRoute{
				{ID: 1, SourceType: "token", SourceID: 9, Model: "claude-3-5", AgentID: "other-model"},
				{ID: 2, SourceType: "token", SourceID: 9, AgentID: "default"},
			},
			model:  "gpt-4o",
			wantID: 2,
		},
		{
			name: "no match",
			routes: []*models.AgentRoute{
				{ID: 1, SourceType: "token", SourceID: 9, Model: "claude-3-5", AgentID: "other-model"},
			},
			model: "gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := NewRouteIndex()
			idx.Load(tt.routes)

			got := idx.FindTokenRoute(9, tt.model)
			if tt.wantID == 0 {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.wantID, got.ID)
		})
	}
}

func TestRouteIndexFindAdminChannelRoute(t *testing.T) {
	tests := []struct {
		name   string
		routes []*models.AgentRoute
		model  string
		wantID uint
	}{
		{
			name: "exact beats earlier default",
			routes: []*models.AgentRoute{
				{ID: 1, SourceType: "channel", SourceID: 9, AgentID: "default", Priority: 100},
				{ID: 2, SourceType: "channel", SourceID: 9, Model: "gpt-4o", AgentID: "exact", Priority: 1},
			},
			model:  "gpt-4o",
			wantID: 2,
		},
		{
			name: "default",
			routes: []*models.AgentRoute{
				{ID: 1, SourceType: "channel", SourceID: 9, Model: "claude-3-5", AgentID: "other-model"},
				{ID: 2, SourceType: "channel", SourceID: 9, AgentID: "default"},
			},
			model:  "gpt-4o",
			wantID: 2,
		},
		{
			name: "no match",
			routes: []*models.AgentRoute{
				{ID: 1, SourceType: "channel", SourceID: 9, Model: "claude-3-5", AgentID: "other-model"},
			},
			model: "gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := NewRouteIndex()
			idx.Load(tt.routes)

			got := idx.FindAdminChannelRoute(9, tt.model)
			if tt.wantID == 0 {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.wantID, got.ID)
		})
	}
}

func TestRouteIndexFindersKeepScopesSeparate(t *testing.T) {
	idx := NewRouteIndex()
	idx.Load([]*models.AgentRoute{
		{ID: 1, SourceType: "token", SourceID: 9, Model: "gpt-4o", AgentID: "token-agent"},
		{ID: 2, SourceType: "channel", SourceID: 9, Model: "gpt-4o", AgentID: "channel-agent"},
	})

	require.Equal(t, uint(1), idx.FindTokenRoute(9, "gpt-4o").ID)
	require.Equal(t, uint(2), idx.FindAdminChannelRoute(9, "gpt-4o").ID)
	require.Nil(t, idx.FindTokenRoute(9, "claude-3-5"))
}

func TestRouteIndexFindersReturnDefensiveCopies(t *testing.T) {
	tests := []struct {
		name      string
		route     *models.AgentRoute
		find      func(*RouteIndex, uint, string) *models.AgentRoute
		sourceID  uint
		realModel string
	}{
		{
			name:  "token",
			route: &models.AgentRoute{ID: 1, SourceType: "token", SourceID: 9, Model: "gpt-4o", AgentID: "token-agent"},
			find: func(idx *RouteIndex, sourceID uint, model string) *models.AgentRoute {
				return idx.FindTokenRoute(sourceID, model)
			},
			sourceID:  9,
			realModel: "gpt-4o",
		},
		{
			name:  "admin channel",
			route: &models.AgentRoute{ID: 2, SourceType: "channel", SourceID: 11, Model: "gpt-4o", AgentID: "channel-agent"},
			find: func(idx *RouteIndex, sourceID uint, model string) *models.AgentRoute {
				return idx.FindAdminChannelRoute(sourceID, model)
			},
			sourceID:  11,
			realModel: "gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := NewRouteIndex()
			idx.Load([]*models.AgentRoute{tt.route})

			first := tt.find(idx, tt.sourceID, tt.realModel)
			require.NotNil(t, first)
			first.AgentID = "mutated"
			first.Model = "other-model"

			again := tt.find(idx, tt.sourceID, tt.realModel)
			require.NotNil(t, again)
			require.Equal(t, tt.route.AgentID, again.AgentID)
			require.Equal(t, tt.realModel, again.Model)
		})
	}
}

func TestRouteIndexFindersAreNilSafe(t *testing.T) {
	idx := &RouteIndex{}

	require.Nil(t, idx.FindTokenRoute(9, "gpt-4o"))
	require.Nil(t, idx.FindAdminChannelRoute(9, "gpt-4o"))
}

func routeIndexStatesEqual(got, want *routeIndexState) bool {
	return got != nil &&
		reflect.DeepEqual(got.routes, want.routes) &&
		reflect.DeepEqual(got.byToken, want.byToken) &&
		reflect.DeepEqual(got.byChannel, want.byChannel)
}

func requireRouteIndexState(
	t *testing.T,
	got *routeIndexState,
	routes map[uint]models.AgentRoute,
	byToken map[uint][]uint,
	byChannel map[uint][]uint,
) {
	t.Helper()
	require.Equal(t, routes, got.routes)
	require.Equal(t, byToken, got.byToken)
	require.Equal(t, byChannel, got.byChannel)
}

func TestRouteIndex_Delete(t *testing.T) {
	ri := NewRouteIndex()
	ri.Put(&models.AgentRoute{ID: 1, SourceType: "token", SourceID: 10, Model: "gpt-4o", AgentTag: "gpu", Priority: 100})
	ri.Delete(1)

	route := ri.FindTokenRoute(10, "gpt-4o")
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
	route := ri.FindTokenRoute(1, "anything")
	if route != nil {
		t.Fatalf("expected old route gone, got %v", route)
	}

	// New data should exist
	route = ri.FindTokenRoute(10, "gpt-4o")
	if route == nil || route.ID != 1 {
		t.Fatalf("expected route 1, got %v", route)
	}
}
