package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type agentRouteSyncCall struct {
	Method  string
	Request protocol.FullSyncRequest
}

type agentRouteSyncClient struct {
	mu      sync.Mutex
	calls   []agentRouteSyncCall
	respond func(context.Context, agentRouteSyncCall, int) (json.RawMessage, error)
}

func (c *agentRouteSyncClient) OnNotification(string, app.NotificationHandler) {}

func (c *agentRouteSyncClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	req, ok := params.(protocol.FullSyncRequest)
	if !ok && method != consts.RPCSyncGetVersion {
		return nil, fmt.Errorf("params type = %T, want protocol.FullSyncRequest", params)
	}
	call := agentRouteSyncCall{Method: method, Request: req}
	c.mu.Lock()
	c.calls = append(c.calls, call)
	callNumber := len(c.calls)
	respond := c.respond
	c.mu.Unlock()
	if respond == nil {
		return nil, errors.New("no scripted response")
	}
	return respond(ctx, call, callNumber)
}

func (c *agentRouteSyncClient) Notify(string, any) error { return nil }
func (c *agentRouteSyncClient) Close() error             { return nil }
func (c *agentRouteSyncClient) ReadLoop()                {}

func (c *agentRouteSyncClient) requests() []agentRouteSyncCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]agentRouteSyncCall(nil), c.calls...)
}

func TestAgentRouteFullSyncRetainsAll501KeysetRows(t *testing.T) {
	firstPage := make([]*models.AgentRoute, 500)
	for i := range firstPage {
		firstPage[i] = testAgentRoute(uint(i+1), fmt.Sprintf("agent-%d", i+1))
	}
	lastPage := []*models.AgentRoute{testAgentRoute(501, "agent-501")}

	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		require.Equal(t, consts.RPCSyncFullSync, call.Method)
		switch callNumber {
		case 1:
			return marshalAgentRouteFullSync(firstPage, protocol.FullSyncResponse{
				Total:         501,
				HasMore:       true,
				Version:       20,
				Keyset:        true,
				LastID:        500,
				SnapshotMaxID: 501,
				BaseVersion:   20,
			}), nil
		case 2:
			return marshalAgentRouteFullSync(lastPage, protocol.FullSyncResponse{
				Total:         501,
				Version:       20,
				Keyset:        true,
				LastID:        501,
				SnapshotMaxID: 501,
				BaseVersion:   20,
			}), nil
		default:
			return nil, fmt.Errorf("unexpected call %d", callNumber)
		}
	}

	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(999, "old")})
	require.NoError(t, syncer.fullSyncAgentRoutes(context.Background()))

	require.Equal(t, 501, syncer.Store.RouteIndex.CacheStat().Size)
	require.Equal(t, "agent-1", matchedAgentID(syncer.Store.RouteIndex, 1))
	require.Equal(t, "agent-501", matchedAgentID(syncer.Store.RouteIndex, 501))
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 999))
	require.False(t, syncer.agentRouteDirty.Load())

	requests := client.requests()
	require.Len(t, requests, 2)
	require.Zero(t, requests[0].Request.Page, "new Agent handshake must omit page zero")
	require.Zero(t, requests[0].Request.AfterID)
	require.Zero(t, requests[0].Request.SnapshotMaxID)
	require.Zero(t, requests[0].Request.BaseVersion)
	require.Zero(t, requests[1].Request.Page)
	require.Equal(t, uint(500), requests[1].Request.AfterID)
	require.Equal(t, uint(501), requests[1].Request.SnapshotMaxID)
	require.Equal(t, int64(20), requests[1].Request.BaseVersion)
}

func TestAgentRouteFullSyncLegacyPagesAccumulateBeforeReplace(t *testing.T) {
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		switch callNumber {
		case 1:
			return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(1, "first")}, protocol.FullSyncResponse{
				Total: 2, Page: 1, HasMore: true, Version: 30,
			}), nil
		case 2:
			require.Equal(t, 2, call.Request.Page)
			return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(2, "second")}, protocol.FullSyncResponse{
				Total: 2, Page: 2, Version: 30,
			}), nil
		default:
			return nil, fmt.Errorf("unexpected call %d", callNumber)
		}
	}

	syncer := newAgentRouteTestSyncer(client)
	require.NoError(t, syncer.fullSyncAgentRoutes(context.Background()))
	require.Equal(t, 2, syncer.Store.RouteIndex.CacheStat().Size)
	require.Equal(t, "first", matchedAgentID(syncer.Store.RouteIndex, 1))
	require.Equal(t, "second", matchedAgentID(syncer.Store.RouteIndex, 2))

	requests := client.requests()
	require.Len(t, requests, 2)
	require.Zero(t, requests[0].Request.Page)
	require.Equal(t, 2, requests[1].Request.Page)
}

func TestAgentRouteFullSyncPageTwoFailureKeepsOldCompleteIndex(t *testing.T) {
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		if callNumber == 1 {
			return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(1, "partial")}, protocol.FullSyncResponse{
				Total: 2, HasMore: true, Version: 40, Keyset: true,
				LastID: 1, SnapshotMaxID: 2, BaseVersion: 40,
			}), nil
		}
		return nil, errors.New("page two unavailable")
	}

	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(900, "old-complete")})
	syncer.Store.SetVersion(12)
	err := syncer.fullSyncAgentRoutes(context.Background())
	require.ErrorContains(t, err, "page two unavailable")
	require.Equal(t, 1, syncer.Store.RouteIndex.CacheStat().Size)
	require.Equal(t, "old-complete", matchedAgentID(syncer.Store.RouteIndex, 900))
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 1))
	require.Equal(t, int64(12), syncer.Store.Version())
	require.True(t, syncer.agentRouteDirty.Load())
}

func TestAgentRouteFullSyncReplaysInsertCapturedBeforePageOneReturns(t *testing.T) {
	client := &agentRouteSyncClient{}
	syncer := newAgentRouteTestSyncer(client)
	client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		if callNumber != 1 {
			return nil, fmt.Errorf("unexpected call %d", callNumber)
		}
		late := testAgentRoute(2, "late-push")
		if err := syncer.applySyncPush(testAgentRoutePush(events.ActionCreate, late, 31)); err != nil {
			return nil, err
		}
		return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(1, "snapshot")}, protocol.FullSyncResponse{
			Total: 1, Version: 31, Keyset: true, LastID: 1,
			SnapshotMaxID: 1, BaseVersion: 30,
		}), nil
	}

	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(900, "old")})
	require.NoError(t, syncer.fullSyncAgentRoutes(context.Background()))
	require.Equal(t, "snapshot", matchedAgentID(syncer.Store.RouteIndex, 1))
	require.Equal(t, "late-push", matchedAgentID(syncer.Store.RouteIndex, 2))
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 900))
	require.Equal(t, int64(31), syncer.Store.Version())
}

func TestAgentRouteFullSyncReplaysGappedPushesInVersionOrder(t *testing.T) {
	client := &agentRouteSyncClient{}
	syncer := newAgentRouteTestSyncer(client)
	client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		if callNumber != 1 {
			return nil, fmt.Errorf("unexpected call %d", callNumber)
		}
		deleteTarget := testAgentRoute(2, "snapshot-delete")
		created := testAgentRoute(3, "created")
		updated := testAgentRoute(3, "updated")
		for _, push := range []protocol.SyncPushParams{
			testAgentRoutePush(events.ActionDelete, deleteTarget, 50),
			testAgentRoutePush(events.ActionUpdate, updated, 80),
			testAgentRoutePush(events.ActionCreate, created, 70),
		} {
			if err := syncer.applySyncPush(push); err != nil {
				return nil, err
			}
		}
		return marshalAgentRouteFullSync([]*models.AgentRoute{deleteTarget}, protocol.FullSyncResponse{
			Version: 80, Keyset: true, LastID: 2, SnapshotMaxID: 2, BaseVersion: 10,
		}), nil
	}

	// Arrival order leaves route 2 deleted and route 3 at "created". The frozen
	// snapshot restores route 2, so only replay can delete it; sorted replay must
	// also apply 70/create before 80/update to leave route 3 at "updated".
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(2, "old")})
	require.NoError(t, syncer.fullSyncAgentRoutes(context.Background()))
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 2))
	require.Equal(t, "updated", matchedAgentID(syncer.Store.RouteIndex, 3))
	require.Equal(t, int64(80), syncer.Store.Version())
	require.False(t, syncer.agentRouteDirty.Load())
}

func TestBuildFinalAgentRoutesReplaysKnownActionsInVersionOrder(t *testing.T) {
	base := []*models.AgentRoute{
		testAgentRoute(4, "unchanged"),
		testAgentRoute(2, "base-update"),
		testAgentRoute(1, "base-delete"),
	}
	pushes := []bufferedAgentRoutePush{
		{action: events.ActionUpdate, route: *testAgentRoute(2, "updated"), version: 80},
		{action: "unknown", route: *testAgentRoute(4, "must-not-apply"), version: 90},
		{action: events.ActionCreate, route: *testAgentRoute(3, "created"), version: 70},
		{action: events.ActionCreate, route: *testAgentRoute(5, "at-base-version"), version: 20},
		{action: events.ActionDelete, route: *testAgentRoute(1, "base-delete"), version: 50},
		{action: events.ActionUpdate, route: *testAgentRoute(2, "older-than-base"), version: 10},
	}

	got := buildFinalAgentRoutes(base, pushes, 20)

	require.Equal(t, []uint{2, 3, 4}, agentRouteSliceIDs(got), "materialized routes must be deterministic")
	require.Equal(t, "updated", got[0].AgentID)
	require.Equal(t, "created", got[1].AgentID)
	require.Equal(t, "unchanged", got[2].AgentID)
	require.Equal(t, "base-update", base[1].AgentID, "helper must not mutate the snapshot")
	require.Equal(t, int64(80), pushes[0].version, "helper must not reorder the buffered input")
}

func TestAgentRouteFullSyncPublishesSnapshotAndReplayAsOneState(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previousProcs)

	syncer := newAgentRouteTestSyncer(&agentRouteSyncClient{})
	oldRoutes := []*models.AgentRoute{testAgentRoute(9000, "old-complete")}
	baseRoutes := []*models.AgentRoute{
		testAgentRoute(1, "snapshot-delete"),
		testAgentRoute(2, "snapshot-update"),
	}
	pushes := []bufferedAgentRoutePush{
		{action: events.ActionUpdate, route: *testAgentRoute(2, "replayed-update"), version: 5000},
		{action: events.ActionDelete, route: *testAgentRoute(1, "snapshot-delete"), version: 1},
	}
	finalRoutes := map[uint]string{2: "replayed-update"}
	for id := uint(3); id <= 2050; id++ {
		route := testAgentRoute(id, fmt.Sprintf("created-%d", id))
		pushes = append(pushes, bufferedAgentRoutePush{
			action:  events.ActionCreate,
			route:   *route,
			version: int64(5000 - id),
		})
		finalRoutes[id] = route.AgentID
	}

	syncer.Store.RouteIndex.Replace(oldRoutes)
	builder := &agentRouteSyncBuilder{
		routes: baseRoutes, pushes: pushes, baseVersion: 0,
		session: syncer.CurrentControlSession(),
	}
	syncer.agentRouteStateMu.Lock()
	syncer.agentRouteBuilder = builder
	syncer.agentRouteStateMu.Unlock()

	oldState := map[uint]string{9000: "old-complete"}
	start := make(chan struct{})
	stop := make(chan struct{})
	invalidState := make(chan string, 1)
	var readersReady sync.WaitGroup
	var readersDone sync.WaitGroup
	const readerCount = 8
	readersReady.Add(readerCount)
	readersDone.Add(readerCount)
	for i := 0; i < readerCount; i++ {
		go func() {
			defer readersDone.Done()
			readersReady.Done()
			<-start
			for {
				select {
				case <-stop:
					return
				default:
				}
				state := syncer.Store.RouteIndex.state.Load()
				if !routeIndexStateMatches(state, oldState) && !routeIndexStateMatches(state, finalRoutes) {
					select {
					case invalidState <- fmt.Sprintf("observed route state with %d entries", len(state.routes)):
					default:
					}
					return
				}
				runtime.Gosched()
			}
		}()
	}
	readersReady.Wait()

	finalizeDone := make(chan error, 1)
	go func() {
		<-start
		finalizeDone <- syncer.finalizeAgentRouteBuilder(context.Background(), builder, 5000)
	}()
	close(start)
	runtime.Gosched()
	require.NoError(t, <-finalizeDone)
	close(stop)
	readersDone.Wait()

	select {
	case observed := <-invalidState:
		t.Fatal(observed)
	default:
	}
	require.True(t, routeIndexStateMatches(syncer.Store.RouteIndex.state.Load(), finalRoutes))
}

func TestAgentRouteFullSyncPushBufferOverflowPreservesLiveUpdates(t *testing.T) {
	client := &agentRouteSyncClient{}
	syncer := newAgentRouteTestSyncer(client)
	client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		if callNumber != 1 {
			return nil, fmt.Errorf("unexpected call %d", callNumber)
		}
		for i := 0; i < maxAgentRoutePushBuffer; i++ {
			live := testAgentRoute(1, fmt.Sprintf("live-%d", i))
			if err := syncer.applySyncPush(testAgentRoutePush(events.ActionUpdate, live, int64(101+i))); err != nil {
				return nil, fmt.Errorf("push %d before capacity: %w", i+1, err)
			}
		}
		last := testAgentRoute(1, "live-4096")
		if err := syncer.applySyncPush(testAgentRoutePush(events.ActionUpdate, last, 4197)); !errors.Is(err, errAgentRoutePushBufferOverflow) {
			return nil, fmt.Errorf("push 4097 error = %v, want %v", err, errAgentRoutePushBufferOverflow)
		}
		return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(2, "partial-snapshot")}, protocol.FullSyncResponse{
			Total: 1, Version: 4197, Keyset: true, LastID: 2,
			SnapshotMaxID: 2, BaseVersion: 100,
		}), nil
	}

	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "old")})
	err := syncer.fullSyncAgentRoutes(context.Background())
	require.ErrorIs(t, err, errAgentRoutePushBufferOverflow)
	require.Equal(t, 1, syncer.Store.RouteIndex.CacheStat().Size)
	require.Equal(t, "live-4096", matchedAgentID(syncer.Store.RouteIndex, 1))
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 2))
	require.Equal(t, int64(4197), syncer.Store.Version())
	require.True(t, syncer.agentRouteDirty.Load())
	requireAgentRouteBuilderCleared(t, syncer)
}

func TestAgentRouteFullSyncRejectsInvalidKeysetCursorWithoutPublishing(t *testing.T) {
	tests := []struct {
		name          string
		invalidLastID uint
	}{
		{name: "last id does not advance", invalidLastID: 5},
		{name: "last id exceeds snapshot maximum", invalidLastID: 11},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &agentRouteSyncClient{}
			client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
				switch callNumber {
				case 1:
					return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(5, "page-one")}, protocol.FullSyncResponse{
						Total: 2, HasMore: true, Version: 20, Keyset: true,
						LastID: 5, SnapshotMaxID: 10, BaseVersion: 20,
					}), nil
				case 2:
					return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(6, "invalid-page")}, protocol.FullSyncResponse{
						Total: 2, HasMore: true, Version: 21, Keyset: true,
						LastID: tc.invalidLastID, SnapshotMaxID: 10, BaseVersion: 20,
					}), nil
				default:
					return nil, fmt.Errorf("unexpected continuation call %d", callNumber)
				}
			}

			syncer := newAgentRouteTestSyncer(client)
			syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "old-complete")})
			syncer.Store.SetVersion(12)
			err := syncer.fullSyncAgentRoutes(context.Background())
			require.ErrorContains(t, err, "cursor")
			require.Len(t, client.requests(), 2)
			require.Equal(t, "old-complete", matchedAgentID(syncer.Store.RouteIndex, 1))
			require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 5))
			require.Equal(t, int64(12), syncer.Store.Version())
			requireAgentRouteBuilderCleared(t, syncer)

			require.NoError(t, syncer.applySyncPush(testAgentRoutePush(
				events.ActionUpdate,
				testAgentRoute(1, "after-cursor-error"),
				30,
			)))
			require.Equal(t, "after-cursor-error", matchedAgentID(syncer.Store.RouteIndex, 1))
		})
	}
}

func TestAgentRouteFullSyncOlderPageResponseCannotMoveVersionBackwards(t *testing.T) {
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		switch callNumber {
		case 1:
			return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(1, "first")}, protocol.FullSyncResponse{
				Total: 2, HasMore: true, Version: 100, Keyset: true,
				LastID: 1, SnapshotMaxID: 2, BaseVersion: 80,
			}), nil
		case 2:
			return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(2, "second")}, protocol.FullSyncResponse{
				Total: 2, Version: 90, Keyset: true,
				LastID: 2, SnapshotMaxID: 2, BaseVersion: 80,
			}), nil
		default:
			return nil, fmt.Errorf("unexpected call %d", callNumber)
		}
	}

	syncer := newAgentRouteTestSyncer(client)
	require.NoError(t, syncer.fullSyncAgentRoutes(context.Background()))
	require.Equal(t, int64(100), syncer.Store.Version())
}

func TestAgentRouteFullSyncFailureStaysDirtyUntilSuccessfulPublication(t *testing.T) {
	sentinel := errors.New("agent route snapshot unavailable")
	var fail atomic.Bool
	fail.Store(true)
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
		require.Equal(t, events.EntityAgentRoute, call.Request.Entity)
		if fail.Load() {
			return nil, sentinel
		}
		return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(2, "recovered")}, protocol.FullSyncResponse{
			Version: 42, Keyset: true, LastID: 2, SnapshotMaxID: 2, BaseVersion: 41,
		}), nil
	}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "old")})

	require.ErrorIs(t, syncer.fullSyncAgentRoutes(context.Background()), sentinel)
	require.True(t, syncer.agentRouteDirty.Load())
	require.Equal(t, "old", matchedAgentID(syncer.Store.RouteIndex, 1))

	fail.Store(false)
	require.NoError(t, syncer.fullSyncAgentRoutes(context.Background()))
	require.False(t, syncer.agentRouteDirty.Load())
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 1))
	require.Equal(t, "recovered", matchedAgentID(syncer.Store.RouteIndex, 2))
}

func TestAgentRouteFullSyncWaitingFailureCannotBeClearedByActiveSuccess(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)

	firstCallStarted := make(chan struct{})
	releaseFirstCall := make(chan struct{})
	sentinel := errors.New("waiting route sync failed")
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		switch callNumber {
		case 1:
			close(firstCallStarted)
			<-releaseFirstCall
			return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(1, "active-success")}, protocol.FullSyncResponse{
				Version: 20, Keyset: true, LastID: 1, SnapshotMaxID: 1, BaseVersion: 19,
			}), nil
		case 2:
			return nil, sentinel
		default:
			return nil, fmt.Errorf("unexpected route sync call %d", callNumber)
		}
	}
	syncer := newAgentRouteTestSyncer(client)

	firstDone := make(chan error, 1)
	go func() { firstDone <- syncer.fullSyncAgentRoutes(context.Background()) }()
	<-firstCallStarted

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondDone <- syncer.fullSyncAgentRoutes(context.Background())
	}()
	<-secondStarted
	// With one P, yielding lets the second pass run until it blocks behind the
	// first pass's serialization mutex.
	runtime.Gosched()
	close(releaseFirstCall)

	require.NoError(t, <-firstDone)
	require.ErrorIs(t, <-secondDone, sentinel)
	require.True(t, syncer.agentRouteDirty.Load())
}

func TestFullSyncReturnsAgentRouteFailureAfterOtherEntitiesAdvanceVersion(t *testing.T) {
	sentinel := errors.New("agent route page failed")
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
		require.Equal(t, consts.RPCSyncFullSync, call.Method)
		if call.Request.Entity == events.EntityAgentRoute {
			return nil, sentinel
		}
		return marshalAgentRouteFullSync(nil, protocol.FullSyncResponse{Version: 99}), nil
	}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "old-complete")})

	err := syncer.FullSync(context.Background())

	require.ErrorIs(t, err, sentinel)
	require.Equal(t, int64(99), syncer.Store.Version(), "earlier entity responses may already expose the remote version")
	require.True(t, syncer.agentRouteDirty.Load())
	require.Equal(t, "old-complete", matchedAgentID(syncer.Store.RouteIndex, 1))
}

func TestAgentRouteDirtySameVersionCheckRetriesAndRetainsFailure(t *testing.T) {
	sentinel := errors.New("route retry failed")
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
		if call.Method == consts.RPCSyncGetVersion {
			return marshalGetVersion(55), nil
		}
		if call.Request.Entity == events.EntityAgentRoute {
			return nil, sentinel
		}
		return marshalAgentRouteFullSync(nil, protocol.FullSyncResponse{Version: 55}), nil
	}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.SetVersion(55)
	syncer.agentRouteDirty.Store(true)

	syncer.checkVersion(context.Background())

	require.Equal(t, 1, countAgentRouteFullSyncCalls(client.requests()))
	require.True(t, syncer.agentRouteDirty.Load())
}

func TestAgentRouteDirtySameVersionCheckClearsAfterSuccessfulPublication(t *testing.T) {
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, call agentRouteSyncCall, _ int) (json.RawMessage, error) {
		if call.Method == consts.RPCSyncGetVersion {
			return marshalGetVersion(55), nil
		}
		return marshalAgentRouteFullSync(nil, protocol.FullSyncResponse{Version: 55}), nil
	}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.SetVersion(55)
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "stale")})
	syncer.agentRouteDirty.Store(true)

	syncer.checkVersion(context.Background())

	require.Equal(t, 1, countAgentRouteFullSyncCalls(client.requests()))
	require.False(t, syncer.agentRouteDirty.Load())
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 1))
}

func TestAgentRouteMalformedOrUnknownPushMarksRoutesDirty(t *testing.T) {
	syncer := newAgentRouteTestSyncer(&agentRouteSyncClient{})

	err := syncer.applySyncPush(protocol.SyncPushParams{
		Entity: events.EntityAgentRoute,
		Action: events.ActionUpdate,
		Data:   json.RawMessage(`{"id":`),
	})
	require.ErrorContains(t, err, "decode agent route push")
	require.True(t, syncer.agentRouteDirty.Load())

	syncer.agentRouteDirty.Store(false)
	err = syncer.applySyncPush(testAgentRoutePush("unknown", testAgentRoute(1, "lost"), 60))
	require.ErrorContains(t, err, "unknown agent route push action")
	require.True(t, syncer.agentRouteDirty.Load())
}

func TestAgentRouteMalformedPushDuringFullSyncAbortsPublicationAndStaysDirty(t *testing.T) {
	client := &agentRouteSyncClient{}
	var syncer *Syncer
	client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		require.Equal(t, 1, callNumber)
		err := syncer.applySyncPush(protocol.SyncPushParams{
			Entity: events.EntityAgentRoute,
			Action: events.ActionUpdate,
			Data:   json.RawMessage(`{"id":`),
		})
		require.ErrorContains(t, err, "decode agent route push")
		return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(2, "must-not-publish")}, protocol.FullSyncResponse{
			Version: 21, Keyset: true, LastID: 2, SnapshotMaxID: 2, BaseVersion: 20,
		}), nil
	}
	syncer = newAgentRouteTestSyncer(client)
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "old-complete")})

	err := syncer.fullSyncAgentRoutes(context.Background())

	require.ErrorContains(t, err, "decode agent route push")
	require.True(t, syncer.agentRouteDirty.Load())
	require.Equal(t, "old-complete", matchedAgentID(syncer.Store.RouteIndex, 1))
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 2))
}

func TestAgentRouteFullSyncCancellationDiscardsBuilder(t *testing.T) {
	callStarted := make(chan struct{})
	client := &agentRouteSyncClient{}
	client.respond = func(ctx context.Context, _ agentRouteSyncCall, _ int) (json.RawMessage, error) {
		close(callStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "old")})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- syncer.fullSyncAgentRoutes(ctx) }()
	<-callStarted
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.True(t, syncer.agentRouteDirty.Load())

	// A post-cancellation push must be applied only to the live index and must
	// survive a subsequent successful replacement/replay cycle independently.
	require.NoError(t, syncer.applySyncPush(testAgentRoutePush(
		events.ActionUpdate,
		testAgentRoute(1, "after-cancel"),
		10,
	)))
	require.Equal(t, "after-cancel", matchedAgentID(syncer.Store.RouteIndex, 1))
}

func TestAgentRouteFullSyncCancellationAfterFinalResponsePreservesOldIndex(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &agentRouteSyncClient{}
	client.respond = func(_ context.Context, _ agentRouteSyncCall, callNumber int) (json.RawMessage, error) {
		if callNumber != 1 {
			return nil, fmt.Errorf("unexpected call %d", callNumber)
		}
		cancel()
		return marshalAgentRouteFullSync([]*models.AgentRoute{testAgentRoute(2, "cancelled-snapshot")}, protocol.FullSyncResponse{
			Total: 1, Version: 20, Keyset: true, LastID: 2,
			SnapshotMaxID: 2, BaseVersion: 10,
		}), nil
	}
	syncer := newAgentRouteTestSyncer(client)
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "old")})

	require.ErrorIs(t, syncer.fullSyncAgentRoutes(ctx), context.Canceled)
	require.Equal(t, "old", matchedAgentID(syncer.Store.RouteIndex, 1))
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 2))
	require.True(t, syncer.agentRouteDirty.Load())
}

func TestAgentRouteFullSyncCancellationWhileFinalizerWaitsPreservesOldIndex(t *testing.T) {
	syncer := newAgentRouteTestSyncer(&agentRouteSyncClient{})
	syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "old")})
	syncer.Store.SetVersion(12)
	builder := &agentRouteSyncBuilder{
		routes:      []*models.AgentRoute{testAgentRoute(2, "cancelled-snapshot")},
		baseVersion: 10,
		session:     syncer.CurrentControlSession(),
	}
	syncer.agentRouteStateMu.Lock()
	syncer.agentRouteBuilder = builder
	syncer.agentRouteStateMu.Unlock()

	baseCtx, cancel := context.WithCancel(context.Background())
	ctx := &finalizerBarrierContext{
		Context:      baseCtx,
		firstErrCall: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}

	syncer.agentRouteStateMu.Lock()
	done := make(chan error, 1)
	go func() {
		err := syncer.finalizeAgentRouteBuilder(ctx, builder, 20)
		syncer.clearAgentRouteBuilder(builder)
		done <- err
	}()
	<-ctx.firstErrCall
	close(ctx.releaseFirst)
	cancel()
	syncer.agentRouteStateMu.Unlock()

	require.ErrorIs(t, <-done, context.Canceled)
	require.Equal(t, "old", matchedAgentID(syncer.Store.RouteIndex, 1))
	require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 2))
	require.Equal(t, int64(12), syncer.Store.Version())
	requireAgentRouteBuilderCleared(t, syncer)
}

type finalizerBarrierContext struct {
	context.Context
	firstErrCall chan struct{}
	releaseFirst chan struct{}
	errCalls     atomic.Int64
}

func (c *finalizerBarrierContext) Err() error {
	if c.errCalls.Add(1) == 1 {
		close(c.firstErrCall)
		<-c.releaseFirst
		return nil
	}
	return c.Context.Err()
}

func newAgentRouteTestSyncer(client app.WSClient) *Syncer {
	return NewSyncer(
		NewStore(nil, config.AgentCacheConfig{}),
		client,
		eventbus.NewMemoryBus(),
		zap.NewNop(),
		time.Hour,
	)
}

func testAgentRoute(id uint, agentID string) *models.AgentRoute {
	return &models.AgentRoute{
		ID:         id,
		SourceType: "token",
		SourceID:   id,
		Model:      "model",
		AgentID:    agentID,
		Priority:   100,
	}
}

func testAgentRoutePush(action string, route *models.AgentRoute, version int64) protocol.SyncPushParams {
	data, err := json.Marshal(route)
	if err != nil {
		panic(err)
	}
	return protocol.SyncPushParams{
		Entity:  events.EntityAgentRoute,
		Action:  action,
		Data:    append([]byte(nil), data...),
		Version: version,
	}
}

func marshalAgentRouteFullSync(routes []*models.AgentRoute, resp protocol.FullSyncResponse) json.RawMessage {
	items, err := json.Marshal(routes)
	if err != nil {
		panic(err)
	}
	resp.Items = items
	raw, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return raw
}

func marshalGetVersion(version int64) json.RawMessage {
	raw, err := json.Marshal(protocol.GetVersionResponse{Version: version})
	if err != nil {
		panic(err)
	}
	return raw
}

func countAgentRouteFullSyncCalls(calls []agentRouteSyncCall) int {
	count := 0
	for _, call := range calls {
		if call.Method == consts.RPCSyncFullSync && call.Request.Entity == events.EntityAgentRoute {
			count++
		}
	}
	return count
}

func agentRouteSliceIDs(routes []*models.AgentRoute) []uint {
	ids := make([]uint, 0, len(routes))
	for _, route := range routes {
		if route != nil {
			ids = append(ids, route.ID)
		}
	}
	return ids
}

func routeIndexStateMatches(state *routeIndexState, expected map[uint]string) bool {
	if state == nil || len(state.routes) != len(expected) {
		return false
	}
	for id, agentID := range expected {
		route, ok := state.routes[id]
		if !ok || route.AgentID != agentID {
			return false
		}
	}
	return true
}

func matchedAgentID(index *RouteIndex, sourceID uint) string {
	route := index.FindTokenRoute(sourceID, "model")
	if route == nil {
		return ""
	}
	return route.AgentID
}

func requireAgentRouteBuilderCleared(t *testing.T, syncer *Syncer) {
	t.Helper()
	syncer.agentRouteStateMu.Lock()
	defer syncer.agentRouteStateMu.Unlock()
	require.Nil(t, syncer.agentRouteBuilder)
}
