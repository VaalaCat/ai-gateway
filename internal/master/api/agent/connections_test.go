package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	masteroperations "github.com/VaalaCat/ai-gateway/internal/master/operations"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type apiControlSource struct {
	facts map[string]connectivity.ControlSessionFact
}

type apiDatabaseOperationFinder struct{ application app.Application }

func (f apiDatabaseOperationFinder) FindAgent(ctx context.Context, agentID string) (models.Agent, error) {
	var agent models.Agent
	err := f.application.GetDB().WithContext(ctx).Where("agent_id = ?", agentID).First(&agent).Error
	return agent, err
}

type apiHandlerControlOperator struct{ handler *Handler }

func (o apiHandlerControlOperator) CallSessionContext(ctx context.Context, agentID string, generation uint64, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if o.handler.HubCallSession == nil {
		return nil, errors.New("hub call session is not available")
	}
	return o.handler.HubCallSession(agentID, generation, method, params, timeout)
}

func attachTestOperations(handler *Handler, ctx *app.Context) {
	handler.Operations = masteroperations.NewService(ctx.RequestContext(),
		apiDatabaseOperationFinder{application: ctx.App},
		masteroperations.Sources{Connections: handler.Connections, Control: apiHandlerControlOperator{handler: handler}},
	)
}

func (s *apiControlSource) GetControlSession(agentID string) (connectivity.ControlSessionFact, bool) {
	fact, ok := s.facts[agentID]
	return fact, ok
}

func TestConnectionsProjectorsPreserveIdentitySortAndCopy(t *testing.T) {
	snapshot := connectivity.ConnectionSnapshot{
		Version:       "v1",
		SnapshotEpoch: "epoch-a",
		SnapshotSeq:   7,
		ObservedAt:    123,
		AgentID:       "source",
		Control: connectivity.ControlSnapshot{
			State:       "connected",
			ReasonCodes: []string{"clock_skew"},
		},
		Relay: connectivity.RelaySnapshot{
			Support:             "supported",
			Config:              "configured",
			Availability:        "ready",
			AcceptingNewStreams: true,
			Convergence:         "converged",
			Active:              connectivity.RelayActiveSnapshot{Streams: 4},
			RecentErrors: []connectivity.RecentError{{
				Code: "relay_secret_error",
			}},
		},
		Direct: connectivity.DirectSnapshot{
			Summary: connectivity.DirectSummary{State: "degraded", Total: 2},
			Targets: map[string]connectivity.DirectTargetSnapshot{
				"map-z": {
					TargetAgentID: "target-z",
					Addresses: []connectivity.DirectAddressSnapshot{{
						URL: "http://z.example",
					}},
					LastError:    &connectivity.RecentError{Code: "last-z"},
					RecentErrors: []connectivity.RecentError{{Code: "recent-z"}},
				},
				"map-a": {
					TargetAgentID: "target-a",
					Addresses: []connectivity.DirectAddressSnapshot{{
						URL: "http://a.example",
					}},
					LastError:    &connectivity.RecentError{Code: "last-a"},
					RecentErrors: []connectivity.RecentError{{Code: "recent-a"}},
				},
			},
		},
		TargetSummaries: connectivity.RouteTargetSummaries{
			Direct: connectivity.DirectSummary{State: "degraded", Total: 2},
		},
		RouteTargets: connectivity.RouteTargetsSnapshot{
			Generation: 7,
			Summaries: connectivity.RouteTargetSummaries{
				Direct: connectivity.DirectSummary{State: "degraded", Total: 2},
			},
			Targets: map[string]connectivity.RouteTargetSnapshot{
				"map-z": {
					TargetAgentID: "target-z",
					Direct: connectivity.RouteDirectTargetSnapshot{
						Addresses: []connectivity.DirectAddressSnapshot{{URL: "http://z.example"}},
						LastError: &connectivity.RecentError{Code: "last-z"},
					},
				},
				"map-a": {
					TargetAgentID: "target-a",
					Direct: connectivity.RouteDirectTargetSnapshot{
						Addresses: []connectivity.DirectAddressSnapshot{{URL: "http://a.example"}},
						LastError: &connectivity.RecentError{Code: "last-a"},
					},
				},
			},
		},
		AllowedOperations: []connectivity.OperationStatus{{
			Operation: connectivity.OperationProbe,
			Allowed:   true,
		}},
	}

	summary := connectionSummary(snapshot)
	require.Equal(t, "v1", summary.Version)
	require.Equal(t, "epoch-a", summary.SnapshotEpoch)
	require.Equal(t, uint64(7), summary.SnapshotSeq)
	require.Equal(t, int64(123), summary.ObservedAt)
	require.Equal(t, 4, summary.Relay.Streams)
	require.Equal(t, snapshot.Direct.Summary, summary.Direct)
	require.Equal(t, snapshot.TargetSummaries, summary.Targets)
	summary.Control.ReasonCodes[0] = "mutated"
	require.Equal(t, []string{"clock_skew"}, snapshot.Control.ReasonCodes)

	encoded, err := json.Marshal(summary)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "relay_secret_error")
	require.Contains(t, string(encoded), "targets")
	require.NotContains(t, string(encoded), "allowed_operations")

	page := routeTargetsPage(snapshot, 0)
	require.Equal(t, "epoch-a", page.SnapshotEpoch)
	require.Equal(t, uint64(7), page.SnapshotSeq)
	require.Equal(t, int64(123), page.ObservedAt)
	require.Equal(t, 20, page.Limit)
	require.NotNil(t, page.Data)
	require.Equal(t, []string{"target-a", "target-z"}, []string{
		page.Data[0].TargetAgentID,
		page.Data[1].TargetAgentID,
	})

	page.Data[0].Direct.Addresses[0].URL = "mutated"
	page.Data[0].Direct.LastError.Code = "mutated"
	require.Equal(t, "http://a.example", snapshot.RouteTargets.Targets["map-a"].Direct.Addresses[0].URL)
	require.Equal(t, "last-a", snapshot.RouteTargets.Targets["map-a"].Direct.LastError.Code)

	maxPage := routeTargetsPage(snapshot, 101)
	require.Equal(t, 100, maxPage.Limit)
	emptyPage := routeTargetsPage(connectivity.ConnectionSnapshot{
		SnapshotEpoch: "epoch-b",
		SnapshotSeq:   8,
		ObservedAt:    124,
	}, -1)
	require.NotNil(t, emptyPage.Data)
	require.Empty(t, emptyPage.Data)
	require.Equal(t, 20, emptyPage.Limit)
}

func TestConnectionSnapshotRuntimeProjectionPreservesNilAndCopies(t *testing.T) {
	withoutStats := fromAgentRuntime(&msync.AgentRuntime{})
	require.Nil(t, withoutStats.CacheStats)

	runtime := &msync.AgentRuntime{CacheStats: map[string]protocol.CacheEntityStats{
		"token": {Hits: 3, Extra: map[string]int64{"hot": 2}},
	}}
	projected := fromAgentRuntime(runtime)
	projectedStat := projected.CacheStats["token"]
	projectedStat.Hits = 99
	projectedStat.Extra["hot"] = 99
	projected.CacheStats["token"] = projectedStat
	require.Equal(t, int64(3), runtime.CacheStats["token"].Hits)
	require.Equal(t, int64(2), runtime.CacheStats["token"].Extra["hot"])
}

func TestOperationGuardErrorMappingIsStableAndDoesNotLeak(t *testing.T) {
	denied := operationAPIError(&connectivity.OperationDeniedError{
		Operation:  connectivity.OperationProbe,
		DenialCode: connectivity.DenialControlDisconnected,
	}, connectivity.OperationProbe)
	require.Equal(t, http.StatusConflict, denied.Status)
	require.Equal(t, connectivity.DenialControlDisconnected, denied.Code)
	require.Equal(t, connectivity.OperationProbe, denied.Details["operation"])
	require.NotContains(t, denied.Message, "denied")

	stale := operationAPIError(
		fmt.Errorf("transport detail: %w", connectivity.ErrConnectionGenerationChanged),
		connectivity.OperationInterrupt,
	)
	require.Equal(t, http.StatusConflict, stale.Status)
	require.Equal(t, connectivity.ErrorCodeConnectionGenerationChanged, stale.Code)
	require.NotContains(t, stale.Message, "transport detail")

	unexpected := operationAPIError(errors.New("secret upstream body"), connectivity.OperationFullSync)
	require.Equal(t, http.StatusInternalServerError, unexpected.Status)
	require.Empty(t, unexpected.Code)
	require.NotContains(t, unexpected.Message, "secret upstream body")
}

func requireAPIError(t *testing.T, err error) *api.APIError {
	t.Helper()
	require.Error(t, err)
	var apiErr *api.APIError
	require.ErrorAs(t, err, &apiErr)
	return apiErr
}

type apiProbeOperator struct {
	ack   protocol.ProbeAck
	scope protocol.ProbeScope
	calls atomic.Int32
}

func (p *apiProbeOperator) EnqueueManualSession(_ context.Context, _ string, _ uint64, scope protocol.ProbeScope) (protocol.ProbeAck, error) {
	p.scope = scope
	p.calls.Add(1)
	return p.ack, nil
}

func TestOperationGuardProbeEnqueuesSchedulerAndReturnsFullAck(t *testing.T) {
	newProbeFixture := func(t *testing.T, status int) (*Handler, *app.Context, *models.Agent, *apiProbeOperator) {
		t.Helper()
		db := setupTestDB(t)
		source := &models.Agent{AgentID: "probe-source", Name: "source", Status: status}
		require.NoError(t, db.Create(source).Error)
		if status == consts.StatusDisabled {
			require.NoError(t, db.Model(source).Update("status", status).Error)
		}
		control := &apiControlSource{facts: map[string]connectivity.ControlSessionFact{
			source.AgentID: {Generation: 77, ConnectedAt: 900, HeartbeatAt: 990},
		}}
		connections := connectivity.NewService("epoch-probe", connectivity.Sources{Control: control}, connectivity.Options{})
		ctx := newTestContext(t, db)
		probes := &apiProbeOperator{ack: protocol.ProbeAck{
			ProbeID: "probe-7", ProbeGeneration: 3, Scope: protocol.ProbeScope{Kind: "tag", Tag: "wan"},
			State: "queued", TargetTotal: 2, SnapshotSeq: 19,
		}}
		handler := &Handler{Connections: connections}
		handler.Operations = masteroperations.NewService(t.Context(),
			apiDatabaseOperationFinder{application: ctx.App},
			masteroperations.Sources{Connections: connections, Probes: probes},
		)
		return handler, ctx, source, probes
	}

	t.Run("full probe ack", func(t *testing.T) {
		handler, ctx, source, probes := newProbeFixture(t, consts.StatusEnabled)
		scope := protocol.ProbeScope{Kind: "tag", Tag: "wan"}
		ack, err := handler.CheckConnectivity(ctx, ConnectivityRequest{
			ID: strconv.Itoa(int(source.ID)), Scope: scope,
			ExpectedEpoch: "epoch-probe", ExpectedControlGeneration: 77,
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusAccepted, ack.HTTPStatus())
		require.Equal(t, probes.ack, ack.Body)
		require.Equal(t, scope, probes.scope)
		require.Equal(t, int32(1), probes.calls.Load())
	})

	t.Run("disabled agent never reaches scheduler", func(t *testing.T) {
		handler, ctx, source, probes := newProbeFixture(t, consts.StatusDisabled)
		_, err := handler.CheckConnectivity(ctx, ConnectivityRequest{ID: strconv.Itoa(int(source.ID))})
		require.Equal(t, connectivity.DenialAgentDisabled, requireAPIError(t, err).Code)
		require.Zero(t, probes.calls.Load())
	})

	t.Run("stale generation never reaches scheduler", func(t *testing.T) {
		handler, ctx, source, probes := newProbeFixture(t, consts.StatusEnabled)
		_, err := handler.CheckConnectivity(ctx, ConnectivityRequest{
			ID: strconv.Itoa(int(source.ID)), ExpectedEpoch: "epoch-probe", ExpectedControlGeneration: 76,
		})
		require.Equal(t, connectivity.ErrorCodeConnectionGenerationChanged, requireAPIError(t, err).Code)
		require.Zero(t, probes.calls.Load())
	})

	t.Run("canceled request never reaches scheduler", func(t *testing.T) {
		handler, ctx, source, probes := newProbeFixture(t, consts.StatusEnabled)
		requestCtx, cancel := context.WithCancel(ctx.Request.Context())
		cancel()
		ctx.Request = ctx.Request.WithContext(requestCtx)
		_, err := handler.CheckConnectivity(ctx, ConnectivityRequest{ID: strconv.Itoa(int(source.ID))})
		require.Equal(t, http.StatusRequestTimeout, requireAPIError(t, err).Status)
		require.Zero(t, probes.calls.Load())
	})
}

func newFullSyncFixture(t *testing.T, count int) (*Handler, *app.Context, []string, *apiControlSource) {
	t.Helper()
	db := setupTestDB(t)
	ids := make([]string, 0, count)
	control := &apiControlSource{facts: make(map[string]connectivity.ControlSessionFact, count)}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("sync-%02d", i)
		agent := models.Agent{AgentID: id, Name: id, Status: consts.StatusEnabled}
		require.NoError(t, db.Create(&agent).Error)
		ids = append(ids, id)
		control.facts[id] = connectivity.ControlSessionFact{
			Generation:  uint64(100 + i),
			ConnectedAt: 900,
			HeartbeatAt: 990,
		}
	}
	h := &Handler{Connections: connectivity.NewService(
		"epoch-full-sync",
		connectivity.Sources{Control: control},
		connectivity.Options{Now: func() time.Time { return time.Unix(1_000, 0) }},
	)}
	ctx := newTestContext(t, db)
	attachTestOperations(h, ctx)
	return h, ctx, ids, control
}

func TestOperationGuardFullSyncPreauthorizesDeduplicatesAndUsesLease(t *testing.T) {
	t.Run("denial prevents every rpc", func(t *testing.T) {
		h, ctx, ids, _ := newFullSyncFixture(t, 2)
		require.NoError(t, ctx.App.GetDB().Model(&models.Agent{}).
			Where("agent_id = ?", ids[1]).Update("status", consts.StatusDisabled).Error)
		h.GetOnlineAgentIDs = func() []string { return append([]string{}, ids...) }
		var calls atomic.Int32
		h.HubCallSession = func(string, uint64, string, any, time.Duration) (json.RawMessage, error) {
			calls.Add(1)
			return json.RawMessage(`{"version":1,"duration_ms":1}`), nil
		}

		_, err := h.FullSync(ctx, FullSyncRequest{AgentIDs: []string{ids[0], ids[0], ids[1]}})
		apiErr := requireAPIError(t, err)
		require.Equal(t, http.StatusConflict, apiErr.Status)
		require.Equal(t, connectivity.DenialAgentDisabled, apiErr.Code)
		require.Equal(t, connectivity.OperationFullSync, apiErr.Details["operation"])
		require.Equal(t, int32(0), calls.Load())
	})

	t.Run("explicit ids are external stable and independent of online candidates", func(t *testing.T) {
		h, ctx, ids, control := newFullSyncFixture(t, 2)
		h.GetOnlineAgentIDs = func() []string { return nil }
		gotGenerations := make(map[string]uint64)
		var mu sync.Mutex
		h.HubCallSession = func(agentID string, generation uint64, _ string, _ any, _ time.Duration) (json.RawMessage, error) {
			mu.Lock()
			gotGenerations[agentID] = generation
			mu.Unlock()
			return json.Marshal(protocol.ForceFullSyncResponse{Version: int64(generation), DurationMs: 9})
		}

		resp, err := h.FullSync(ctx, FullSyncRequest{AgentIDs: []string{ids[1], ids[0], ids[1]}})
		require.NoError(t, err)
		require.Equal(t, []string{ids[1], ids[0]}, []string{resp.Results[0].AgentID, resp.Results[1].AgentID})
		require.True(t, resp.Results[0].Success)
		require.True(t, resp.Results[1].Success)
		require.Equal(t, int64(control.facts[ids[1]].Generation), resp.Results[0].Version)
		require.Equal(t, int64(control.facts[ids[0]].Generation), resp.Results[1].Version)
		require.Equal(t, control.facts[ids[0]].Generation, gotGenerations[ids[0]])
		require.Equal(t, control.facts[ids[1]].Generation, gotGenerations[ids[1]])
	})
}

func TestOperationGuardFullSyncConcurrencyIsBoundedAndOrdered(t *testing.T) {
	h, ctx, ids, _ := newFullSyncFixture(t, 33)
	candidates := append([]string{}, ids...)
	candidates = append(candidates, ids[5])
	h.GetOnlineAgentIDs = func() []string { return append([]string{}, candidates...) }

	release := make(chan struct{})
	started := make(chan struct{}, len(ids))
	var active atomic.Int32
	var maximum atomic.Int32
	var calls atomic.Int32
	h.HubCallSession = func(agentID string, generation uint64, _ string, _ any, _ time.Duration) (json.RawMessage, error) {
		calls.Add(1)
		current := active.Add(1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		active.Add(-1)
		return json.Marshal(protocol.ForceFullSyncResponse{Version: int64(generation), DurationMs: 1})
	}

	type fullSyncOutcome struct {
		response FullSyncResponse
		err      error
	}
	done := make(chan fullSyncOutcome, 1)
	go func() {
		response, err := h.FullSync(ctx, FullSyncRequest{All: true})
		done <- fullSyncOutcome{response: response, err: err}
	}()
	for i := 0; i < 16; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("full-sync did not fill its bounded worker set")
		}
	}
	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}
	require.LessOrEqual(t, maximum.Load(), int32(16))
	close(release)

	var outcome fullSyncOutcome
	select {
	case outcome = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("full-sync workers did not converge")
	}
	require.NoError(t, outcome.err)
	require.Equal(t, int32(len(ids)), calls.Load())
	require.LessOrEqual(t, maximum.Load(), int32(16))
	require.Len(t, outcome.response.Results, len(ids))
	for i, result := range outcome.response.Results {
		require.Equal(t, ids[i], result.AgentID)
		require.True(t, result.Success)
	}
}

func TestOperationGuardFullSyncOrdinaryFailuresStayIsolatedAndRedacted(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		err  error
		want string
	}{
		{name: "rpc failure", err: errors.New("secret full-sync upstream body"), want: "agent operation failed"},
		{name: "invalid response", raw: json.RawMessage(`secret invalid response`), want: "invalid agent response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ctx, ids, _ := newFullSyncFixture(t, 1)
			h.HubCallSession = func(string, uint64, string, any, time.Duration) (json.RawMessage, error) {
				return tt.raw, tt.err
			}

			response, err := h.FullSync(ctx, FullSyncRequest{AgentIDs: ids})
			require.NoError(t, err)
			require.Len(t, response.Results, 1)
			require.False(t, response.Results[0].Success)
			require.Equal(t, tt.want, response.Results[0].Error)
			require.NotContains(t, response.Results[0].Error, "secret")
		})
	}
}

func TestOperationGuardFullSyncStaleGenerationIsTopLevelAfterWorkersConverge(t *testing.T) {
	h, ctx, ids, _ := newFullSyncFixture(t, 4)
	h.GetOnlineAgentIDs = func() []string { return append([]string{}, ids...) }
	release := make(chan struct{})
	var arrived atomic.Int32
	var finished atomic.Int32
	h.HubCallSession = func(agentID string, generation uint64, _ string, _ any, _ time.Duration) (json.RawMessage, error) {
		if arrived.Add(1) == int32(len(ids)) {
			close(release)
		}
		<-release
		defer finished.Add(1)
		if agentID == ids[1] {
			return nil, fmt.Errorf("private stale detail: %w", connectivity.ErrConnectionGenerationChanged)
		}
		return json.Marshal(protocol.ForceFullSyncResponse{Version: int64(generation)})
	}

	_, err := h.FullSync(ctx, FullSyncRequest{All: true})
	apiErr := requireAPIError(t, err)
	require.Equal(t, http.StatusConflict, apiErr.Status)
	require.Equal(t, connectivity.ErrorCodeConnectionGenerationChanged, apiErr.Code)
	require.NotContains(t, apiErr.Message, "private stale detail")
	require.Equal(t, int32(len(ids)), finished.Load())
}

func TestOperationGuardFullSyncCancellationDuringCallReturnsTopLevelError(t *testing.T) {
	h, ctx, ids, _ := newFullSyncFixture(t, 1)
	h.GetOnlineAgentIDs = func() []string { return append([]string{}, ids...) }
	requestCtx, cancel := context.WithCancel(ctx.Request.Context())
	ctx.Request = ctx.Request.WithContext(requestCtx)
	started := make(chan struct{})
	release := make(chan struct{})
	h.HubCallSession = func(_ string, generation uint64, _ string, _ any, _ time.Duration) (json.RawMessage, error) {
		close(started)
		<-release
		return json.Marshal(protocol.ForceFullSyncResponse{Version: int64(generation)})
	}

	type outcome struct {
		response FullSyncResponse
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		response, err := h.FullSync(ctx, FullSyncRequest{All: true})
		done <- outcome{response: response, err: err}
	}()
	<-started
	cancel()
	close(release)
	result := <-done
	apiErr := requireAPIError(t, result.err)
	require.Equal(t, http.StatusRequestTimeout, apiErr.Status)
}

func TestOperationGuardInterruptAuthorizesAndUsesLease(t *testing.T) {
	newInterruptFixture := func(t *testing.T, status int) (*Handler, *app.Context, *models.Agent, *apiControlSource) {
		t.Helper()
		db := setupTestDB(t)
		agent := &models.Agent{AgentID: "interrupt-agent", Name: "interrupt", Status: consts.StatusEnabled}
		require.NoError(t, db.Create(agent).Error)
		if status == consts.StatusDisabled {
			require.NoError(t, db.Model(agent).Update("status", status).Error)
			agent.Status = status
		}
		control := &apiControlSource{facts: map[string]connectivity.ControlSessionFact{
			agent.AgentID: {Generation: 55, ConnectedAt: 900, HeartbeatAt: 990},
		}}
		h := &Handler{Connections: connectivity.NewService(
			"epoch-interrupt",
			connectivity.Sources{Control: control},
			connectivity.Options{Now: func() time.Time { return time.Unix(1_000, 0) }},
		)}
		ctx := newTestContext(t, db)
		attachTestOperations(h, ctx)
		return h, ctx, agent, control
	}

	t.Run("authorized generation is passed", func(t *testing.T) {
		h, ctx, agent, _ := newInterruptFixture(t, consts.StatusEnabled)
		var gotGeneration uint64
		var gotMethod string
		var gotParams any
		h.HubCallSession = func(_ string, generation uint64, method string, params any, _ time.Duration) (json.RawMessage, error) {
			gotGeneration, gotMethod, gotParams = generation, method, params
			return json.RawMessage(`{"interrupted":true}`), nil
		}

		response, err := h.Interrupt(ctx, InterruptRequest{AgentID: agent.ID, ID: 9})
		require.NoError(t, err)
		require.True(t, response.Interrupted)
		require.Equal(t, uint64(55), gotGeneration)
		require.Equal(t, consts.RPCAgentInterrupt, gotMethod)
		require.Equal(t, map[string]any{"id": int64(9)}, gotParams)
	})

	t.Run("denial and cancellation prevent rpc", func(t *testing.T) {
		t.Run("denied", func(t *testing.T) {
			h, ctx, agent, _ := newInterruptFixture(t, consts.StatusDisabled)
			var calls atomic.Int32
			h.HubCallSession = func(string, uint64, string, any, time.Duration) (json.RawMessage, error) {
				calls.Add(1)
				return json.RawMessage(`{"interrupted":true}`), nil
			}
			_, err := h.Interrupt(ctx, InterruptRequest{AgentID: agent.ID, ID: 9})
			apiErr := requireAPIError(t, err)
			require.Equal(t, connectivity.DenialAgentDisabled, apiErr.Code)
			require.Equal(t, int32(0), calls.Load())
		})

		t.Run("canceled", func(t *testing.T) {
			h, ctx, agent, _ := newInterruptFixture(t, consts.StatusEnabled)
			requestCtx, cancel := context.WithCancel(ctx.Request.Context())
			cancel()
			ctx.Request = ctx.Request.WithContext(requestCtx)
			var calls atomic.Int32
			h.HubCallSession = func(string, uint64, string, any, time.Duration) (json.RawMessage, error) {
				calls.Add(1)
				return json.RawMessage(`{"interrupted":true}`), nil
			}
			_, err := h.Interrupt(ctx, InterruptRequest{AgentID: agent.ID, ID: 9})
			apiErr := requireAPIError(t, err)
			require.Equal(t, http.StatusRequestTimeout, apiErr.Status)
			require.Equal(t, int32(0), calls.Load())
		})
	})

	t.Run("generation race is a stable conflict", func(t *testing.T) {
		h, ctx, agent, control := newInterruptFixture(t, consts.StatusEnabled)
		h.HubCallSession = func(_ string, generation uint64, _ string, _ any, _ time.Duration) (json.RawMessage, error) {
			control.facts[agent.AgentID] = connectivity.ControlSessionFact{Generation: generation + 1}
			return nil, fmt.Errorf("private interrupt detail: %w", connectivity.ErrConnectionGenerationChanged)
		}
		_, err := h.Interrupt(ctx, InterruptRequest{AgentID: agent.ID, ID: 9})
		apiErr := requireAPIError(t, err)
		require.Equal(t, connectivity.ErrorCodeConnectionGenerationChanged, apiErr.Code)
		require.NotContains(t, apiErr.Message, "private interrupt detail")
	})

	t.Run("nil dependencies fail closed", func(t *testing.T) {
		h, ctx, agent, _ := newInterruptFixture(t, consts.StatusEnabled)
		h.Connections = nil
		h.HubCallSession = nil
		h.Operations = nil
		_, err := h.Interrupt(ctx, InterruptRequest{AgentID: agent.ID, ID: 9})
		require.Equal(t, http.StatusInternalServerError, requireAPIError(t, err).Status)
	})
}

func TestConnectionSnapshotDiagnosticsUseCurrentGenerationWithoutOperationAuthorization(t *testing.T) {
	newDiagnosticFixture := func(t *testing.T, connected bool) (*Handler, *app.Context, *models.Agent) {
		t.Helper()
		db := setupTestDB(t)
		agent := &models.Agent{AgentID: "diagnostic-agent", Name: "diagnostic", Status: consts.StatusEnabled, LastSeen: 9_999}
		require.NoError(t, db.Create(agent).Error)
		// Read-only diagnostics have no Operation and must not reuse Interrupt's
		// admin-status authorization.
		require.NoError(t, db.Model(agent).Update("status", consts.StatusDisabled).Error)
		agent.Status = consts.StatusDisabled
		facts := map[string]connectivity.ControlSessionFact{}
		if connected {
			facts[agent.AgentID] = connectivity.ControlSessionFact{Generation: 88, ConnectedAt: 900, HeartbeatAt: 990}
		}
		h := &Handler{Connections: connectivity.NewService(
			"epoch-diagnostics",
			connectivity.Sources{Control: &apiControlSource{facts: facts}},
			connectivity.Options{Now: func() time.Time { return time.Unix(1_000, 0) }},
		)}
		return h, newTestContext(t, db), agent
	}

	t.Run("inflight and goroutines pass the snapshot generation", func(t *testing.T) {
		h, ctx, agent := newDiagnosticFixture(t, true)
		var generations []uint64
		var methods []string
		var mu sync.Mutex
		h.HubCallSession = func(_ string, generation uint64, method string, _ any, _ time.Duration) (json.RawMessage, error) {
			mu.Lock()
			generations = append(generations, generation)
			methods = append(methods, method)
			mu.Unlock()
			return json.RawMessage(`[]`), nil
		}

		inflightRaw, err := h.GetInflight(ctx, AgentIDQuery{ID: strconv.Itoa(int(agent.ID))})
		require.NoError(t, err)
		require.JSONEq(t, `[]`, string(inflightRaw))
		goroutinesRaw, err := h.GetGoroutines(ctx, AgentIDQuery{ID: strconv.Itoa(int(agent.ID))})
		require.NoError(t, err)
		require.JSONEq(t, `[]`, string(goroutinesRaw))
		require.Equal(t, []uint64{88, 88}, generations)
		require.Equal(t, []string{consts.RPCAgentInflight, consts.RPCAgentGoroutines}, methods)
	})

	t.Run("fresh last seen cannot replace a current session", func(t *testing.T) {
		h, ctx, agent := newDiagnosticFixture(t, false)
		var calls atomic.Int32
		h.HubCallSession = func(string, uint64, string, any, time.Duration) (json.RawMessage, error) {
			calls.Add(1)
			return json.RawMessage(`[]`), nil
		}
		_, err := h.GetInflight(ctx, AgentIDQuery{ID: strconv.Itoa(int(agent.ID))})
		apiErr := requireAPIError(t, err)
		require.Equal(t, http.StatusConflict, apiErr.Status)
		require.Equal(t, connectivity.DenialControlDisconnected, apiErr.Code)
		require.Equal(t, int32(0), calls.Load())
	})

	t.Run("stale generation is stable and canceled request performs no rpc", func(t *testing.T) {
		t.Run("stale", func(t *testing.T) {
			h, ctx, agent := newDiagnosticFixture(t, true)
			h.HubCallSession = func(string, uint64, string, any, time.Duration) (json.RawMessage, error) {
				return nil, fmt.Errorf("private diagnostic detail: %w", connectivity.ErrConnectionGenerationChanged)
			}
			_, err := h.GetGoroutines(ctx, AgentIDQuery{ID: strconv.Itoa(int(agent.ID))})
			apiErr := requireAPIError(t, err)
			require.Equal(t, connectivity.ErrorCodeConnectionGenerationChanged, apiErr.Code)
			require.NotContains(t, apiErr.Message, "private diagnostic detail")
		})

		t.Run("canceled", func(t *testing.T) {
			h, ctx, agent := newDiagnosticFixture(t, true)
			requestCtx, cancel := context.WithCancel(ctx.Request.Context())
			cancel()
			ctx.Request = ctx.Request.WithContext(requestCtx)
			var calls atomic.Int32
			h.HubCallSession = func(string, uint64, string, any, time.Duration) (json.RawMessage, error) {
				calls.Add(1)
				return json.RawMessage(`[]`), nil
			}
			_, err := h.GetInflight(ctx, AgentIDQuery{ID: strconv.Itoa(int(agent.ID))})
			require.Equal(t, http.StatusRequestTimeout, requireAPIError(t, err).Status)
			require.Equal(t, int32(0), calls.Load())
		})
	})

	t.Run("nil dependencies fail closed", func(t *testing.T) {
		h, ctx, agent := newDiagnosticFixture(t, true)
		h.Connections = nil
		h.HubCallSession = nil
		_, err := h.GetInflight(ctx, AgentIDQuery{ID: strconv.Itoa(int(agent.ID))})
		require.Equal(t, http.StatusInternalServerError, requireAPIError(t, err).Status)
	})
}

func TestOnlineProjectionEmptyCandidatesBuildsOneBatchWithoutQuery(t *testing.T) {
	db := setupTestDB(t)
	var queries atomic.Int32
	callbackName := "test:online_empty_query:" + t.Name()
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callbackName, func(*gorm.DB) {
		queries.Add(1)
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

	connections := connectivity.NewService("epoch-online-empty", connectivity.Sources{}, connectivity.Options{
		Now: func() time.Time { return time.Unix(1_000, 0) },
	})
	h := &Handler{
		Connections:       connections,
		GetOnlineAgentIDs: func() []string { return nil },
	}
	ctx := newTestContext(t, db)
	before := connections.Build(models.Agent{AgentID: "before"})

	response, err := h.Online(ctx, api.EmptyRequest{})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Empty(t, response)
	after := connections.Build(models.Agent{AgentID: "after"})
	require.Equal(t, before.SnapshotSeq+2, after.SnapshotSeq, "Online must consume exactly one batch sequence")
	require.Zero(t, queries.Load(), "empty online candidates must not query the database")
}

func TestConnectionSnapshotAcrossListDetailOnlineProjection(t *testing.T) {
	db := setupTestDB(t)
	connected := models.Agent{
		AgentID:       "connected-agent",
		Name:          "connected",
		Status:        consts.StatusEnabled,
		LastSeen:      100,
		HTTPAddresses: `[{"url":"http://configured.example","tag":"manual"}]`,
		Tags:          "edge",
	}
	disconnected := models.Agent{
		AgentID:  "disconnected-agent",
		Name:     "disconnected",
		Status:   consts.StatusEnabled,
		LastSeen: 9_999,
	}
	require.NoError(t, db.Create(&connected).Error)
	require.NoError(t, db.Create(&disconnected).Error)

	control := &apiControlSource{facts: map[string]connectivity.ControlSessionFact{
		connected.AgentID: {
			Generation:        41,
			ConnectedAt:       900,
			HeartbeatAt:       990,
			RuntimeReportedAt: 991,
		},
	}}
	connections := connectivity.NewService("epoch-api", connectivity.Sources{Control: control}, connectivity.Options{
		Now: func() time.Time { return time.Unix(1_000, 0) },
	})

	tracker := msync.NewHeartbeatTracker(nil, zap.NewNop(), 0)
	tracker.Touch(connected.AgentID, 995)
	hub := msync.NewHub(nil, zap.NewNop(), nil, func() int64 { return 1 }, nil, msync.HubOptions{})
	hub.Heartbeat = tracker
	ctx := newTestContext(t, db)
	ctx.UserInfo = &app.UserInfo{Role: 2}
	h := &Handler{
		Connections:       connections,
		Hub:               hub,
		GetOnlineAgentIDs: func() []string { return []string{connected.AgentID, disconnected.AgentID} },
		GetRuntime: func(agentID string) *msync.AgentRuntime {
			if agentID != connected.AgentID {
				return nil
			}
			return &msync.AgentRuntime{
				Uptime:       7,
				PendingUsage: 3,
				CacheStats: map[string]protocol.CacheEntityStats{
					"token": {Hits: 2},
				},
			}
		},
	}

	list, err := h.List(ctx, ListRequest{})
	require.NoError(t, err)
	require.Len(t, list.Data, 2)
	require.Equal(t, list.Data[0].Connection.SnapshotEpoch, list.Data[1].Connection.SnapshotEpoch)
	require.Equal(t, list.Data[0].Connection.SnapshotSeq, list.Data[1].Connection.SnapshotSeq)
	require.Equal(t, list.Data[0].Connection.ObservedAt, list.Data[1].Connection.ObservedAt)
	listByAgentID := make(map[string]AgentResponse, len(list.Data))
	for _, row := range list.Data {
		listByAgentID[row.AgentID] = row
	}
	listConnected := listByAgentID[connected.AgentID]
	require.Equal(t, int64(995), listConnected.LastSeen)
	require.Equal(t, int64(995), listConnected.Connection.Control.LastSeen)
	require.Equal(t, "connected", listConnected.Connection.Control.State)
	require.Equal(t, "disconnected", listByAgentID[disconnected.AgentID].Connection.Control.State)
	require.NotEmpty(t, listConnected.HTTPAddresses)
	require.Equal(t, connected.HTTPAddresses, listConnected.ConfiguredHTTPAddresses)
	require.Equal(t, listConnected.HTTPAddresses, listConnected.EffectiveHTTPAddresses)

	detail, err := h.Detail(ctx, DetailRequest{ID: "1"})
	require.NoError(t, err)
	require.Empty(t, detail.Secret)
	require.Equal(t, int64(995), detail.LastSeen)
	require.NotNil(t, detail.Runtime)
	require.Equal(t, 3, detail.Runtime.PendingUsage)
	require.Equal(t, listConnected.Connection.SnapshotEpoch, detail.Connection.SnapshotEpoch)
	require.Greater(t, detail.Connection.SnapshotSeq, listConnected.Connection.SnapshotSeq)
	require.Equal(t, listConnected.Connection.Control, detail.Connection.Control)
	require.Equal(t, detail.Connection.SnapshotEpoch, detail.RouteTargets.SnapshotEpoch)
	require.Equal(t, detail.Connection.RouteTargets.Generation, detail.RouteTargets.SnapshotSeq)
	require.Equal(t, detail.Connection.ObservedAt, detail.RouteTargets.ObservedAt)
	require.NotNil(t, detail.RouteTargets.Data)
	require.Equal(t, 20, detail.RouteTargets.Limit)
	require.Equal(t, "unknown", detail.Connection.Direct.Summary.State)

	online, err := h.Online(ctx, api.EmptyRequest{})
	require.NoError(t, err)
	require.Len(t, online, 1, "fresh last_seen cannot make a disconnected candidate online")
	require.Equal(t, connected.AgentID, online[0].AgentID)
	require.Equal(t, 3, online[0].PendingUsage)
	require.Equal(t, int64(995), online[0].LastSeen)
	require.Equal(t, listConnected.Connection.SnapshotEpoch, online[0].Connection.SnapshotEpoch)
	require.Greater(t, online[0].Connection.SnapshotSeq, detail.Connection.SnapshotSeq)
	require.Equal(t, listConnected.Connection.Control, online[0].Connection.Control)

	direct, err := h.RouteTargets(ctx, RouteTargetsRequest{ID: "1"})
	require.NoError(t, err)
	require.NotNil(t, direct.Data)
	require.Empty(t, direct.Data)
	require.Equal(t, 20, direct.Limit)
	require.Equal(t, "epoch-api", direct.SnapshotEpoch)
	require.Equal(t, detail.Connection.RouteTargets.Generation, direct.SnapshotSeq)
}
