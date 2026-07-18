package agent

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestRouteTargetsCursorKeepsTheOriginalSnapshotAndSortsByTargetAgentID(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, "target-c", "target-a", "target-b")

	first, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID)), Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"target-a", "target-b"}, directTargetIDs(first.Data))
	require.Equal(t, 3, first.Summaries.Direct.Total)
	require.Equal(t, 3, first.Summaries.Relay.Total)
	require.NotEmpty(t, first.NextCursor)

	now = now.Add(time.Minute)
	h.Connections.MarkDirectProbeChecking(agent.AgentID, 1, connectivity.ProbeTarget{
		AgentID: "target-d", Name: "target-d", Addresses: []protocol.Address{{URL: "https://target-d.example"}},
	}, "fp-target-d", 4)
	second, err := h.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Cursor: first.NextCursor, Limit: 2,
		ExpectedSnapshotEpoch: first.SnapshotEpoch, ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	require.NoError(t, err)
	require.Equal(t, first.SnapshotEpoch, second.SnapshotEpoch)
	require.Equal(t, first.SnapshotSeq, second.SnapshotSeq)
	require.Equal(t, first.ObservedAt, second.ObservedAt)
	require.Equal(t, []string{"target-c"}, directTargetIDs(second.Data))
	require.Equal(t, first.Summaries, second.Summaries, "every page must carry summaries for the complete route target snapshot")
	require.Empty(t, second.NextCursor)
}

func TestRouteTargetsExpectedSnapshotRefetchReusesOriginalFirstPage(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, "target-a")
	first, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID)), Limit: 20})
	require.NoError(t, err)
	require.Empty(t, first.NextCursor)
	require.Equal(t, []string{"target-a"}, directTargetIDs(first.Data))

	h.Connections.MarkDirectProbeChecking(agent.AgentID, 1, connectivity.ProbeTarget{
		AgentID: "target-b", Name: "target-b", Addresses: []protocol.Address{{URL: "https://target-b.example"}},
	}, "fp-target-b", 2)
	refetched, err := h.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Limit: 20,
		ExpectedSnapshotEpoch: first.SnapshotEpoch, ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	require.NoError(t, err)
	require.Equal(t, first.SnapshotEpoch, refetched.SnapshotEpoch)
	require.Equal(t, first.SnapshotSeq, refetched.SnapshotSeq)
	require.Equal(t, first.ObservedAt, refetched.ObservedAt)
	require.Equal(t, []string{"target-a"}, directTargetIDs(refetched.Data))
}

func TestRouteTargetsExpectedSnapshotRefetchKeepsOriginalPagedFirstPage(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, "target-a", "target-b")
	first, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID)), Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, first.NextCursor)

	h.Connections.MarkDirectProbeChecking(agent.AgentID, 1, connectivity.ProbeTarget{
		AgentID: "target-0", Name: "target-0", Addresses: []protocol.Address{{URL: "https://target-0.example"}},
	}, "fp-target-0", 3)
	refetched, err := h.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Limit: 1,
		ExpectedSnapshotEpoch: first.SnapshotEpoch, ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	require.NoError(t, err)
	require.Equal(t, first.SnapshotSeq, refetched.SnapshotSeq)
	require.Equal(t, []string{"target-a"}, directTargetIDs(refetched.Data))
	require.Equal(t, first.NextCursor, refetched.NextCursor)
}

func TestRouteTargetsExpectedSnapshotRefetchValidatesCompleteAndAvailableIdentity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, "target-a")
	id := strconv.Itoa(int(agent.ID))
	first, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: id})
	require.NoError(t, err)

	for _, req := range []RouteTargetsRequest{
		{ID: id, ExpectedSnapshotEpoch: first.SnapshotEpoch},
		{ID: id, ExpectedSnapshotSeq: first.SnapshotSeq},
	} {
		_, err = h.RouteTargets(ctx, req)
		apiErr := requireAPIError(t, err)
		require.Equal(t, http.StatusBadRequest, apiErr.Status)
		require.Equal(t, "route_targets_snapshot_invalid", apiErr.Code)
	}

	h.routeTargetsPagesMu.Lock()
	h.routeTargetsPages = nil
	h.routeTargetsPagesMu.Unlock()
	_, err = h.RouteTargets(ctx, RouteTargetsRequest{
		ID: id, ExpectedSnapshotEpoch: first.SnapshotEpoch, ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	apiErr := requireAPIError(t, err)
	require.Equal(t, http.StatusConflict, apiErr.Status)
	require.Equal(t, "route_targets_cursor_snapshot_changed", apiErr.Code)
}

func TestRouteTargetsCursorRejectsExpectedSnapshotMismatch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, "target-a", "target-b")
	first, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID)), Limit: 1})
	require.NoError(t, err)

	_, err = h.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Cursor: first.NextCursor, Limit: 1,
		ExpectedSnapshotEpoch: "epoch-b", ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	apiErr := requireAPIError(t, err)
	require.Equal(t, http.StatusConflict, apiErr.Status)
	require.Equal(t, "route_targets_cursor_epoch_changed", apiErr.Code)

	_, err = h.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Cursor: first.NextCursor, Limit: 1,
		ExpectedSnapshotEpoch: first.SnapshotEpoch, ExpectedSnapshotSeq: first.SnapshotSeq + 1,
	})
	apiErr = requireAPIError(t, err)
	require.Equal(t, http.StatusConflict, apiErr.Status)
	require.Equal(t, "route_targets_cursor_snapshot_changed", apiErr.Code)
}

func TestRouteTargetsCursorRejectsEpochMismatchAndTampering(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	firstHandler, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, "target-a", "target-b")
	first, err := firstHandler.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID)), Limit: 1})
	require.NoError(t, err)

	other := newRouteTargetsService("epoch-b", "source", "target-a", "target-b")
	firstHandler.Connections = other
	_, err = firstHandler.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Cursor: first.NextCursor, Limit: 1,
		ExpectedSnapshotEpoch: "epoch-b", ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	apiErr := requireAPIError(t, err)
	require.Equal(t, "route_targets_cursor_epoch_changed", apiErr.Code)
	require.Equal(t, 409, apiErr.Status)

	firstHandler.Connections = newRouteTargetsService("epoch-a", "source", "target-a", "target-b")
	_, err = firstHandler.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Cursor: first.NextCursor + "tampered", Limit: 1,
		ExpectedSnapshotEpoch: first.SnapshotEpoch, ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	apiErr = requireAPIError(t, err)
	require.Equal(t, "route_targets_cursor_invalid", apiErr.Code)
	require.Equal(t, 400, apiErr.Status)
}

func TestRouteTargetsCursorRejectsEvictedSameEpochSnapshot(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, "target-a", "target-b")
	first, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID)), Limit: 1})
	require.NoError(t, err)

	h.routeTargetsPagesMu.Lock()
	h.routeTargetsPages = nil
	h.routeTargetsPagesMu.Unlock()
	_, err = h.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Cursor: first.NextCursor, Limit: 1,
		ExpectedSnapshotEpoch: first.SnapshotEpoch, ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	apiErr := requireAPIError(t, err)
	require.Equal(t, "route_targets_cursor_snapshot_changed", apiErr.Code)
	require.Equal(t, 409, apiErr.Status)
}

func TestRouteTargetsLimitsDefaultToTwentyAndCapAtOneHundred(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	targets := make([]string, 105)
	for i := range targets {
		targets[i] = fmt.Sprintf("target-%03d", i)
	}
	h, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, targets...)

	defaultPage, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID))})
	require.NoError(t, err)
	require.Equal(t, 20, defaultPage.Limit)
	require.Len(t, defaultPage.Data, 20)

	maxPage, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID)), Limit: 1_000})
	require.NoError(t, err)
	require.Equal(t, 100, maxPage.Limit)
	require.Len(t, maxPage.Data, 100)
}

func TestRouteTargetsSnapshotIdentityChangesOnlyWithRouteTargetContent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, "target-a")
	id := strconv.Itoa(int(agent.ID))

	first, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: id})
	require.NoError(t, err)
	second, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: id})
	require.NoError(t, err)
	require.NotZero(t, first.SnapshotSeq)
	require.Equal(t, first.SnapshotSeq, second.SnapshotSeq)

	h.Connections.MarkDirectProbeChecking(agent.AgentID, 1, connectivity.ProbeTarget{
		AgentID: "target-b", Name: "target-b", Addresses: []protocol.Address{{URL: "https://target-b.example"}},
	}, "fp-target-b", 2)
	changed, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: id})
	require.NoError(t, err)
	require.NotEqual(t, changed.SnapshotSeq, second.SnapshotSeq, "manual probe targets change the visible route target snapshot")
	require.Len(t, changed.Data, 2)

	h.Connections.MarkDirectProbeChecking(agent.AgentID, 1, connectivity.ProbeTarget{
		AgentID: "target-a", Name: "target-a", Addresses: []protocol.Address{{URL: "https://target-a.example"}},
	}, "fp-target-a", 3)
	routeChanged, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: id})
	require.NoError(t, err)
	require.NotEqual(t, routeChanged.SnapshotSeq, changed.SnapshotSeq)
}

func TestConnectionDetailAndRouteTargetsHonorCanceledRequest(t *testing.T) {
	db := setupTestDB(t)
	agent := models.Agent{AgentID: "source", Name: "source", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&agent).Error)
	ctx := newTestContext(t, db)
	canceled, cancel := context.WithCancel(ctx.Request.Context())
	cancel()
	ctx.Request = ctx.Request.WithContext(canceled)
	h := &Handler{Connections: connectivity.NewService("epoch", connectivity.Sources{}, connectivity.Options{})}

	_, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID))})
	require.Equal(t, "request_canceled", requireAPIError(t, err).Code)
	_, err = h.Detail(ctx, DetailRequest{ID: strconv.Itoa(int(agent.ID))})
	require.Equal(t, "request_canceled", requireAPIError(t, err).Code)
}

func TestRouteTargetsCursorTTLBoundaryIsFiveMinutes(t *testing.T) {
	issuedAt := time.Unix(1_700_000_000, 0)
	now := issuedAt
	h, ctx, agent := newRouteTargetsFixture(t, "epoch-a", &now, "target-a", "target-b")
	first, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: strconv.Itoa(int(agent.ID)), Limit: 1})
	require.NoError(t, err)

	now = issuedAt.Add(5 * time.Minute)
	_, err = h.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Cursor: first.NextCursor, Limit: 1,
		ExpectedSnapshotEpoch: first.SnapshotEpoch, ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	require.NoError(t, err, "cursor remains valid through its five-minute boundary")

	now = issuedAt.Add(5*time.Minute + time.Second)
	_, err = h.RouteTargets(ctx, RouteTargetsRequest{
		ID: strconv.Itoa(int(agent.ID)), Cursor: first.NextCursor, Limit: 1,
		ExpectedSnapshotEpoch: first.SnapshotEpoch, ExpectedSnapshotSeq: first.SnapshotSeq,
	})
	apiErr := requireAPIError(t, err)
	require.Equal(t, "route_targets_cursor_expired", apiErr.Code)
	require.Equal(t, 410, apiErr.Status)
}

func TestRouteTargetsSnapshotStoreIsBoundedAndKeepsNewestEntry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := &Handler{Now: func() time.Time { return now }}
	var firstCursor, latestCursor string
	for sequence := uint64(1); sequence <= uint64(maxRouteTargetsSnapshotEntries+1); sequence++ {
		snapshot := connectivity.ConnectionSnapshot{
			SnapshotEpoch: "epoch-a", SnapshotSeq: sequence, ObservedAt: now.Unix(), AgentID: "source",
			RouteTargets: connectivity.RouteTargetsSnapshot{Generation: sequence, Targets: map[string]connectivity.RouteTargetSnapshot{
				"target-a": {TargetAgentID: "target-a"},
				"target-b": {TargetAgentID: "target-b"},
			}},
		}
		page, err := h.routeTargetsPage(snapshot, "", 1)
		require.NoError(t, err)
		require.NotEmpty(t, page.NextCursor)
		if sequence == 1 {
			firstCursor = page.NextCursor
		}
		latestCursor = page.NextCursor
	}

	require.Len(t, h.routeTargetsPages, maxRouteTargetsSnapshotEntries)
	first, err := decodeRouteTargetsCursor(firstCursor)
	require.NoError(t, err)
	_, ok := h.loadRouteTargetsSnapshot(first, now)
	require.False(t, ok)
	latest, err := decodeRouteTargetsCursor(latestCursor)
	require.NoError(t, err)
	loaded, ok := h.loadRouteTargetsSnapshot(latest, now)
	require.True(t, ok)
	require.Equal(t, uint64(maxRouteTargetsSnapshotEntries+1), loaded.SnapshotSeq)
}

func TestRouteTargetsSnapshotStoreRefreshDoesNotEvictAnotherSource(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h := &Handler{Now: func() time.Time { return now }}
	var oldestCursor string
	var newest connectivity.ConnectionSnapshot
	for index := 0; index < maxRouteTargetsSnapshotEntries; index++ {
		sourceID := fmt.Sprintf("source-%02d", index)
		snapshot := connectivity.ConnectionSnapshot{
			SnapshotEpoch: "epoch-a", SnapshotSeq: uint64(index + 1), ObservedAt: now.Unix(), AgentID: sourceID,
			RouteTargets: connectivity.RouteTargetsSnapshot{Generation: 1, Targets: map[string]connectivity.RouteTargetSnapshot{
				"target-a": {TargetAgentID: "target-a"},
				"target-b": {TargetAgentID: "target-b"},
			}},
		}
		page, err := h.routeTargetsPage(snapshot, "", 1)
		require.NoError(t, err)
		require.NotEmpty(t, page.NextCursor)
		if index == 0 {
			oldestCursor = page.NextCursor
		}
		newest = snapshot
	}
	require.Len(t, h.routeTargetsPages, maxRouteTargetsSnapshotEntries)

	_, err := h.routeTargetsPage(newest, "", 1)
	require.NoError(t, err)
	require.Len(t, h.routeTargetsPages, maxRouteTargetsSnapshotEntries)

	oldest, err := decodeRouteTargetsCursor(oldestCursor)
	require.NoError(t, err)
	loaded, ok := h.loadRouteTargetsSnapshot(oldest, now)
	require.True(t, ok)
	require.Equal(t, "source-00", loaded.AgentID)
}

func newRouteTargetsFixture(t *testing.T, epoch string, now *time.Time, targets ...string) (*Handler, *app.Context, models.Agent) {
	t.Helper()
	db := setupTestDB(t)
	agent := models.Agent{AgentID: "source", Name: "source", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&agent).Error)
	return &Handler{
		Connections: newRouteTargetsService(epoch, agent.AgentID, targets...),
		Now:         func() time.Time { return *now },
	}, newTestContext(t, db), agent
}

func newRouteTargetsService(epoch, sourceID string, targetIDs ...string) *connectivity.Service {
	service := connectivity.NewService(epoch, connectivity.Sources{}, connectivity.Options{})
	events := make([]protocol.RouteEvent, 0, len(targetIDs))
	for generation, targetID := range targetIDs {
		target := connectivity.ProbeTarget{AgentID: targetID, Name: targetID, Addresses: []protocol.Address{{URL: "https://" + targetID + ".example"}}}
		service.MarkDirectProbeChecking(sourceID, 1, target, "fp-"+targetID, uint64(generation+1))
		events = append(events, protocol.RouteEvent{
			TargetAgentID: targetID, RouteID: uint(generation + 1), SelectorKind: "agent",
			PathKind: "direct", Result: "success", ObservedAt: time.Now().Unix(), Sequence: uint64(generation + 1),
		})
	}
	_ = service.ApplyEvents(sourceID, protocol.RouteTelemetryBatch{Generation: 1, Events: events})
	return service
}

func directTargetIDs(targets []connectivity.RouteTargetSnapshot) []string {
	ids := make([]string, len(targets))
	for i, target := range targets {
		ids[i] = target.TargetAgentID
	}
	return ids
}
