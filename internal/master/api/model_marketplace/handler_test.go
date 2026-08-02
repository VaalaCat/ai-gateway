package model_marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	relayplan "github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/plan"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestHandlerUsesAgentPlannerQuotaFactsInsteadOfDAOAvailability(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	t.Cleanup(store.Close)
	store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 11, Status: consts.StatusEnabled, Weight: 1,
			Endpoints: `{"chat_completions":"/v1/chat/completions"}`,
		},
		Models: "gpt-4o",
	})
	store.SetModelConfig(&models.ModelConfig{ModelName: "gpt-4o", InputPrice: 1})
	store.SetUser(&protocol.SyncedUser{ID: 7, GroupID: 2, Quota: 10})
	store.LoadSettings([]models.Setting{
		{Key: consts.SettingKeyMinQuotaReserve, Value: "100"},
		{Key: consts.SettingKeyBYOKBillingMode, Value: consts.BYOKBillingModeServiceFee},
	})
	store.RebuildModelIndex()

	handler := testMarketplaceHandler(t, &fakeHandlerGate{user: scopedMarketplaceViewer(7, 23)})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{
		handlerMarketplaceModel(
			"gpt-4o", "GPT-4o", "OpenAI", nil, nil,
			handlerOffer(t, "gpt-4o", OfferKindPlatform, 11),
		),
	}}
	handler.SetModelOfferPlanFinder(relayplan.NewModelOfferPlanFinder(store))

	response, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
	require.NoError(t, err)
	require.Empty(t, response.Models, "quota-rejected Relay candidates must not remain marketplace-available")
}

func TestHandlerFailsClosedBeforeCatalogWhenPlannerIsUnavailable(t *testing.T) {
	calls := []string{}
	handler := testMarketplaceHandler(t, &fakeHandlerGate{
		user: scopedMarketplaceViewer(7, 23), calls: &calls,
	})
	handler.SetModelOfferPlanFinder(nil)
	handler.catalog = &fakeHandlerCatalog{
		calls: &calls,
		models: []MarketplaceModel{handlerMarketplaceModel(
			"gpt-4o", "GPT-4o", "OpenAI", nil, nil,
			handlerOffer(t, "gpt-4o", OfferKindPlatform, 11),
		)},
	}

	response, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
	require.NoError(t, err)
	require.Empty(t, response.Models)
	require.Equal(t, []string{"gate:user"}, calls)

	t.Run("admin keeps diagnostics unavailable", func(t *testing.T) {
		admin := testMarketplaceHandler(t, &fakeHandlerGate{admin: MarketplaceViewer{AdminGlobal: true}})
		admin.SetModelOfferPlanFinder(nil)
		admin.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{
			handlerMarketplaceModel(
				"gpt-4o", "GPT-4o", "OpenAI", nil, nil,
				handlerOffer(t, "gpt-4o", OfferKindPlatform, 11),
			),
		}}

		response, adminErr := admin.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{})
		require.NoError(t, adminErr)
		require.Len(t, response.Models, 1)
		require.Len(t, response.Models[0].Real.Offers, 1)
		require.False(t, response.Models[0].Real.Offers[0].Available)
		require.Zero(t, response.Models[0].Real.AvailableOfferCount)
	})
}

func TestHandlerJoinsPlannerCandidatesByCompleteSourceIdentity(t *testing.T) {
	viewer := scopedMarketplaceViewer(7, 23)
	platform11 := handlerOffer(t, "gpt-4o", OfferKindPlatform, 11)
	platform12 := handlerOffer(t, "gpt-4o", OfferKindPlatform, 12)
	private12 := handlerOffer(t, "gpt-4o", OfferKindPrivate, 12)
	model := handlerMarketplaceModel(
		"gpt-4o", "GPT-4o", "OpenAI", nil, nil,
		platform11, platform12, private12,
	)
	planFinder := &fakeHandlerOfferPlanFinder{plans: map[string]relayplan.ModelOfferPlan{
		"gpt-4o": {
			RequestModel: "gpt-4o", RealModels: []string{"gpt-4o"},
			Candidates: []relayplan.ModelOfferCandidate{{
				RealModel: "gpt-4o", Source: state.SourceAdmin, SourceID: 12,
				Modes: handlerOfferPlanModes(),
			}},
		},
	}}

	t.Run("ordinary viewer sees only the planned identity", func(t *testing.T) {
		handler := testMarketplaceHandler(t, &fakeHandlerGate{user: viewer})
		handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{model}}
		handler.SetModelOfferPlanFinder(planFinder)

		response, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
		require.NoError(t, err)
		require.Len(t, response.Models, 1)
		require.Len(t, response.Models[0].Real.Offers, 1)
		require.Equal(t, platform12.OfferRef, response.Models[0].Real.Offers[0].OfferRef)
		require.True(t, response.Models[0].Real.Offers[0].Available)
	})

	t.Run("admin keeps diagnostics but only planned identity is available", func(t *testing.T) {
		handler := testMarketplaceHandler(t, &fakeHandlerGate{admin: MarketplaceViewer{AdminGlobal: true}})
		handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{model}}
		handler.SetModelOfferPlanFinder(planFinder)

		response, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{})
		require.NoError(t, err)
		require.Len(t, response.Models, 1)
		require.Len(t, response.Models[0].Real.Offers, 3)
		available := make(map[string]bool, 3)
		for _, offer := range response.Models[0].Real.Offers {
			available[offer.OfferRef] = offer.Available
		}
		require.False(t, available[platform11.OfferRef])
		require.True(t, available[platform12.OfferRef])
		require.False(t, available[private12.OfferRef])
	})
}

func TestHandlerUsesAgentPlanForRoutingReachabilityAndSameNamePriority(t *testing.T) {
	aOffer := handlerOffer(t, "model-a", OfferKindPlatform, 11)
	bOffer := handlerOffer(t, "model-b", OfferKindPrivate, 21)
	catalog := []MarketplaceModel{
		handlerMarketplaceModel("model-a", "A", "Provider", nil, nil, aOffer),
		handlerMarketplaceModel("model-b", "B", "Provider", nil, nil, bOffer),
	}
	route := RoutingModel{
		ModelName: "smart", DisplayName: "Smart",
		ReachableRealModels: []string{"model-a"},
		FlattenedDestinations: []FlattenedDestination{{
			ModelName: "model-a", Offers: []ModelOfferSummary{routingOfferSummary(aOffer)},
		}},
		RoutingWarnings: []RoutingWarning{}, Guidance: RoutingModelGuidanceViewReachableRealModels,
		Facts: RoutingModelFacts{Definitions: []RoutingDefinitionFact{{
			RoutingID: 31, Name: "smart", Scope: models.RoutingScopeGlobal, Enabled: true,
		}}},
	}
	plans := &fakeHandlerOfferPlanFinder{plans: map[string]relayplan.ModelOfferPlan{
		"model-a": offerPlan("model-a", offerPlanCandidate("model-a", state.SourceAdmin, 11)),
		"model-b": offerPlan("model-b", offerPlanCandidate("model-b", state.SourcePrivate, 21)),
		"smart": {
			RequestModel: "smart", Routing: true, RealModels: []string{"model-b"},
			Candidates: []relayplan.ModelOfferCandidate{
				offerPlanCandidate("model-b", state.SourcePrivate, 21),
			},
		},
	}}
	handler := testMarketplaceHandler(t, &fakeHandlerGate{user: scopedMarketplaceViewer(7, 23)})
	handler.catalog = &fakeHandlerCatalog{models: catalog}
	handler.routing = &fakeHandlerRouting{models: []RoutingModel{route}}
	handler.SetModelOfferPlanFinder(plans)

	response, err := handler.List(userMarketplaceContext(t, 7), ListRequest{
		TokenID: 23, Kind: ModelKindRouting,
	})
	require.NoError(t, err)
	require.Len(t, response.Models, 1)
	require.Equal(t, []string{"model-b"}, response.Models[0].Routing.ReachableRealModels)
	require.Len(t, response.Models[0].Routing.FlattenedDestinations, 1)
	require.Equal(t, bOffer.OfferRef, response.Models[0].Routing.FlattenedDestinations[0].Offers[0].OfferRef)

	t.Run("planner says same name is real even when Master definition says routing", func(t *testing.T) {
		sameOffer := handlerOffer(t, "same", OfferKindPlatform, 41)
		sameRoute := RoutingModel{
			ModelName: "same", DisplayName: "Same route",
			ReachableRealModels: []string{"model-b"},
			Facts: RoutingModelFacts{Definitions: []RoutingDefinitionFact{{
				RoutingID: 42, Name: "same", Scope: models.RoutingScopeGlobal, Enabled: true,
			}}},
		}
		sameHandler := testMarketplaceHandler(t, &fakeHandlerGate{user: scopedMarketplaceViewer(7, 23)})
		sameHandler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{
			handlerMarketplaceModel("same", "Same real", "Provider", nil, nil, sameOffer),
		}}
		sameHandler.routing = &fakeHandlerRouting{models: []RoutingModel{sameRoute}}
		sameHandler.SetModelOfferPlanFinder(&fakeHandlerOfferPlanFinder{plans: map[string]relayplan.ModelOfferPlan{
			"same": offerPlan("same", offerPlanCandidate("same", state.SourceAdmin, 41)),
		}})

		list, listErr := sameHandler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
		require.NoError(t, listErr)
		require.Len(t, list.Models, 1)
		require.Equal(t, ModelKindReal, list.Models[0].Kind)
		require.Equal(t, "same", list.Models[0].Real.ModelName)
	})
}

func TestHandlerTokenPreviewBuildsRelayUserInfoWithoutCollapsingAllowlists(t *testing.T) {
	viewer := scopedMarketplaceViewer(7, 23)
	viewer.GroupID = 9
	viewer.TokenAllowedChannelIDs = []uint{2, 3}
	viewer.GroupAllowedChannelIDs = []uint{1, 2}
	viewer.AllowedChannelIDs = []uint{2}
	viewer.AllowedModels = MarketplaceModelWhitelist{
		TokenPatterns: []string{"gpt-.*"}, GroupPatterns: []string{"gpt-4o"},
	}
	viewer.BYOKOnly = true
	finder := &fakeHandlerOfferPlanFinder{plans: map[string]relayplan.ModelOfferPlan{
		"gpt-4o": offerPlan("gpt-4o", offerPlanCandidate("gpt-4o", state.SourcePrivate, 21)),
	}}
	handler := testMarketplaceHandler(t, &fakeHandlerGate{admin: viewer})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{
		handlerMarketplaceModel(
			"gpt-4o", "GPT", "OpenAI", nil, nil,
			handlerOffer(t, "gpt-4o", OfferKindPrivate, 21),
		),
	}}
	handler.SetModelOfferPlanFinder(finder)

	_, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{TokenID: uintPointer(23)})
	require.NoError(t, err)
	require.Len(t, finder.queries, 1)
	require.Equal(t, &app.UserInfo{
		UserID: 7, TokenID: 23, GroupID: 9, BYOKOnly: true,
		AllowedChannelIDs:      []uint{2, 3},
		GroupAllowedChannelIDs: []uint{1, 2},
		TokenModels:            []string{"gpt-.*"},
		GroupModels:            []string{"gpt-4o"},
	}, finder.queries[0].UserInfo)
	require.Equal(t, marketplaceInboundProtocols, finder.queries[0].InboundProtocols)
}

// Break caught: moving request parsing, catalog work, or performance reads in
// front of the access gate would make the dependency counters non-zero.
func TestHandlerDisabledGateReturns404BeforeEveryDependency(t *testing.T) {
	calls := []string{}
	handler := testMarketplaceHandler(t, &fakeHandlerGate{
		userErr: api.NotFoundError(consts.ErrNotFound),
		calls:   &calls,
	})
	handler.catalog = &fakeHandlerCatalog{calls: &calls}
	handler.routing = &fakeHandlerRouting{calls: &calls}
	handler.performance = &fakeHandlerPerformance{calls: &calls}
	handler.usage = &fakeHandlerUsage{calls: &calls}
	plans := &fakeHandlerOfferPlanFinder{}
	handler.SetModelOfferPlanFinder(plans)

	_, err := handler.List(userMarketplaceContext(t, 7), ListRequest{
		TokenID: 23,
		Kind:    MarketplaceModelKind("not-even-validated"),
	})

	requireMarketplaceAPIError(t, err, http.StatusNotFound, "", consts.ErrNotFound)
	require.Equal(t, []string{"gate:user"}, calls)
	require.Empty(t, plans.queries)
}

// Break caught: projecting before the composed available-offer boundary would
// include unavailable or no-longer-visible cache facts in the model metric.
func TestPerformanceForComposedModelProjectsOnlyVisibleAvailableOfferComponents(t *testing.T) {
	observedUntil := time.Date(2026, 8, 2, 10, 30, 0, 0, time.UTC)
	currentHour := observedUntil.Truncate(time.Hour)
	visible := handlerOffer(t, "gpt-4o", OfferKindPlatform, 1)
	unavailable := handlerOffer(t, "gpt-4o", OfferKindPlatform, 2)
	unavailable.Available = false
	absent := handlerOffer(t, "gpt-4o", OfferKindPlatform, 3)
	batch := ModelPerformanceResult{
		PerformanceStatus: PerformanceAvailable,
		ObservedUntil:     observedUntil,
		Offers: map[string]OfferPerformanceSnapshot{
			visible.OfferRef:     {Summary: PerformanceSummary{Status: MarketplaceHealthOperational}},
			unavailable.OfferRef: {Summary: PerformanceSummary{Status: MarketplaceHealthOutage}},
			absent.OfferRef:      {Summary: PerformanceSummary{Status: MarketplaceHealthOutage}},
		},
		offerComponents: map[string][]HourlyPerformanceComponents{
			visible.OfferRef: {{Hour: currentHour, Components: PerformanceComponents{
				RequestCount: 5, SuccessCount: 5, InputTokens: 70, CacheReadTokens: 30,
			}}},
			unavailable.OfferRef: {{Hour: currentHour, Components: PerformanceComponents{
				RequestCount: 100, SuccessCount: 0, InputTokens: 1_000,
			}}},
			absent.OfferRef: {{Hour: currentHour, Components: PerformanceComponents{
				RequestCount: 100, SuccessCount: 0, InputTokens: 1_000,
			}}},
		},
		projectionWindow: UsageWindow24Hours,
		minSamples:       1,
	}
	model := composedRealModel{offers: []composedOffer{
		{offer: visible}, {offer: unavailable},
	}}

	result, err := performanceForComposedModel(batch, model)

	require.NoError(t, err)
	require.Equal(t, map[string]OfferPerformanceSnapshot{
		visible.OfferRef: batch.Offers[visible.OfferRef],
	}, result.Offers)
	require.NotNil(t, result.Model.SuccessRate)
	require.InDelta(t, 100.0, *result.Model.SuccessRate, 0.000001)
	require.NotNil(t, result.Model.CacheHitRate)
	require.InDelta(t, 30.0, *result.Model.CacheHitRate, 0.000001)
	require.Len(t, result.Model.StatusBuckets, 24)
	require.Equal(t, MarketplaceHealthOperational, result.Model.StatusBuckets[23].Status)

	empty, err := performanceForComposedModel(batch, composedRealModel{})
	require.NoError(t, err)
	require.Nil(t, empty.Model.SuccessRate)
	require.Nil(t, empty.Model.CacheHitRate)
	require.Len(t, empty.Model.StatusBuckets, 24)
	for _, bucket := range empty.Model.StatusBuckets {
		require.Equal(t, MarketplaceHealthUnknown, bucket.Status)
		require.Nil(t, bucket.SuccessRate)
	}
}

// Break caught: a model-only overflow must enter the existing performance
// degradation boundary instead of serializing a successful response with an
// empty status history.
func TestHandlerDegradesCrossOfferModelProjectionOverflow(t *testing.T) {
	observedUntil := time.Date(2026, 8, 2, 10, 30, 0, 0, time.UTC)
	first := handlerOffer(t, "gpt-4o", OfferKindPlatform, 1)
	second := handlerOffer(t, "gpt-4o", OfferKindPlatform, 2)
	handler := testMarketplaceHandler(t, &fakeHandlerGate{user: scopedMarketplaceViewer(7, 23)})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{
		handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, first, second),
	}}
	handler.performance = &fakeHandlerPerformance{result: ModelPerformanceResult{
		PerformanceStatus: PerformanceAvailable,
		ObservedUntil:     observedUntil,
		Offers: map[string]OfferPerformanceSnapshot{
			first.OfferRef:  {Summary: PerformanceSummary{Status: MarketplaceHealthOperational}},
			second.OfferRef: {Summary: PerformanceSummary{Status: MarketplaceHealthOperational}},
		},
		offerComponents: map[string][]HourlyPerformanceComponents{
			first.OfferRef: {{Hour: observedUntil.Truncate(time.Hour), Components: PerformanceComponents{
				RequestCount: math.MaxInt64, SuccessCount: math.MaxInt64,
			}}},
			second.OfferRef: {{Hour: observedUntil.Truncate(time.Hour), Components: PerformanceComponents{
				RequestCount: 1, SuccessCount: 1,
			}}},
		},
		projectionWindow: UsageWindow24Hours,
		minSamples:       1,
	}}

	response, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})

	require.NoError(t, err)
	require.Len(t, response.Models, 1)
	require.Equal(t, MarketplaceHealthUnknown, response.Models[0].Real.AggregateStatus)
	performance := response.Models[0].Real.Performance
	require.Equal(t, UsageWindow24Hours, performance.Window)
	require.Nil(t, performance.SuccessRate)
	require.Nil(t, performance.CacheHitRate)
	require.Len(t, performance.StatusHistory, 24)
	for _, bucket := range performance.StatusHistory {
		require.Equal(t, MarketplaceHealthUnknown, bucket.Status)
		require.Nil(t, bucket.SuccessRate)
	}
	for _, offer := range response.Models[0].Real.Offers {
		require.Equal(t, PerformanceUnavailable, offer.PerformanceStatus)
	}
}

// Break caught: a finder error must preserve the caller-selected window and
// emit its fixed unknown history shape for both directory and detail views.
func TestHandlerPerformanceFailureKeepsRequestedUnknownProjectionShape(t *testing.T) {
	offer := handlerOffer(t, "gpt-4o", OfferKindPlatform, 1)
	handler := testMarketplaceHandler(t, &fakeHandlerGate{user: scopedMarketplaceViewer(7, 23)})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{
		handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, offer),
	}}
	handler.performance = &fakeHandlerPerformance{err: errors.New("performance finder failed")}

	list, listErr := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
	require.NoError(t, listErr)
	require.Len(t, list.Models, 1)
	assertUnknownUserModelPerformance(t, list.Models[0].Real.Performance, UsageWindow24Hours, 24)

	detail, detailErr := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{
		TokenID: 23, Model: "gpt-4o", Window: string(UsageWindow7Days),
	})
	require.NoError(t, detailErr)
	assertUnknownUserModelPerformance(t, detail.Model.Real.Performance, UsageWindow7Days, 28)
}

func assertUnknownUserModelPerformance(t *testing.T, performance UserModelPerformanceDTO, window UsageWindow, bucketCount int) {
	t.Helper()
	require.Equal(t, PerformanceUnavailable, performance.PerformanceStatus)
	require.Equal(t, window, performance.Window)
	require.Nil(t, performance.SuccessRate)
	require.Nil(t, performance.CacheHitRate)
	require.Len(t, performance.StatusHistory, bucketCount)
	for _, bucket := range performance.StatusHistory {
		require.Equal(t, MarketplaceHealthUnknown, bucket.Status)
		require.Nil(t, bucket.SuccessRate)
	}
}

// Break caught: returning a domain result directly, or reusing offer DTOs,
// could expose raw counters or accidentally add performance to routing models.
func TestModelPerformanceDTOsUseExplicitRealModelAllowlists(t *testing.T) {
	successRate := 99.42
	cacheHitRate := 78.30
	model := composedRealModel{
		model: handlerMarketplaceModel("gpt-4o", "GPT-4o", "OpenAI", nil, nil,
			handlerOffer(t, "gpt-4o", OfferKindPlatform, 1)),
		offers: []composedOffer{{offer: handlerOffer(t, "gpt-4o", OfferKindPlatform, 1)}},
		performance: ModelPerformanceResult{
			Model: ModelPerformanceSnapshot{
				PerformanceStatus: PerformanceStale,
				Window:            UsageWindow24Hours, SuccessRate: &successRate, CacheHitRate: &cacheHitRate,
				StatusBuckets: []ModelPerformanceStatusBucket{{
					StartedAt: 1, EndedAt: 2, SuccessRate: &successRate,
					Status: MarketplaceHealthOperational, InProgress: true,
				}},
			},
		},
	}

	user := mapUserRealModel(model)
	admin := mapAdminRealModel(model)
	for _, response := range []any{user, admin} {
		payload, err := json.Marshal(response)
		require.NoError(t, err)
		var body map[string]any
		require.NoError(t, json.Unmarshal(payload, &body))
		real := body["real"].(map[string]any)
		performance, exists := real["performance"].(map[string]any)
		require.True(t, exists)
		require.Equal(t, "stale", performance["performance_status"])
		require.Equal(t, "24h", performance["window"])
		require.InDelta(t, 99.42, performance["success_rate"], 0.000001)
		require.InDelta(t, 78.30, performance["cache_hit_rate"], 0.000001)
		history := performance["status_history"].([]any)
		require.Len(t, history, 1)
		bucket := history[0].(map[string]any)
		require.InDelta(t, 99.42, bucket["success_rate"], 0.000001)
		for _, forbidden := range []string{"request_count", "success_count", "channel_id", "private_channel_id", "offer_ref"} {
			require.NotContains(t, performance, forbidden)
		}
	}

	routingPayload, err := json.Marshal(mapUserRoutingModel(RoutingModel{ModelName: "smart-route"}))
	require.NoError(t, err)
	require.NotContains(t, string(routingPayload), "performance")
}

// Break caught: a failed refresh with a complete cached snapshot must retain
// historical model metrics while exposing stale freshness in both allowlists.
func TestStaleCacheResultReachesComposedUserAndAdminModelPerformanceDTOs(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 30, 0, 0, time.UTC)
	current := now
	loadErr := errors.New("performance refresh failed")
	offer := handlerOffer(t, "gpt-4o", OfferKindPlatform, 41)
	var loads atomic.Int64
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(
		func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
			if loads.Add(1) > 1 {
				return nil, loadErr
			}
			snapshot := performanceFinderTestSnapshot(observedUntil)
			offerSnapshot := snapshot.Offers[offer.Identity]
			offerSnapshot.HourlyTrendComponents30D[len(offerSnapshot.HourlyTrendComponents30D)-1].Components = PerformanceComponents{
				RequestCount: 20, SuccessCount: 20, InputTokens: 10, CacheReadTokens: 10,
			}
			snapshot.Offers[offer.Identity] = offerSnapshot
			return snapshot, nil
		},
	))
	handler := testMarketplaceHandler(t, &fakeHandlerGate{
		user: scopedMarketplaceViewer(7, 23), admin: MarketplaceViewer{AdminGlobal: true},
	})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{
		handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, offer),
	}}
	handler.performance = NewModelPerformanceFinder(cache, handler.settings)
	handler.clock = func() time.Time { return current }

	warm, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
	require.NoError(t, err)
	require.Equal(t, PerformanceAvailable, warm.Models[0].Real.Performance.PerformanceStatus)

	current = now.Add(time.Hour)
	admin, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{})
	require.NoError(t, err)
	user, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
	require.NoError(t, err)
	for _, performance := range []struct {
		status   PerformanceStatus
		sla      *float64
		cacheHit *float64
	}{
		{
			status:   admin.Models[0].Real.Performance.PerformanceStatus,
			sla:      admin.Models[0].Real.Performance.SuccessRate,
			cacheHit: admin.Models[0].Real.Performance.CacheHitRate,
		},
		{
			status:   user.Models[0].Real.Performance.PerformanceStatus,
			sla:      user.Models[0].Real.Performance.SuccessRate,
			cacheHit: user.Models[0].Real.Performance.CacheHitRate,
		},
	} {
		require.Equal(t, PerformanceStale, performance.status)
		require.NotNil(t, performance.sla)
		require.InDelta(t, 100.0, *performance.sla, 0.000001)
		require.NotNil(t, performance.cacheHit)
		require.InDelta(t, 50.0, *performance.cacheHit, 0.000001)
	}
}

// Break caught: filters derived after request filtering would lose Anthropic
// and image modalities, while unstable map iteration would change ordering.
func TestListBuildsViewerWideFiltersBeforeApplyingSearchProviderAndKind(t *testing.T) {
	viewer := scopedMarketplaceViewer(7, 23)
	viewer.Token.Name = "Production"
	gpt := handlerMarketplaceModel("gpt-4o", "GPT-4o", "OpenAI", []string{"text", "image"}, []string{"text"},
		handlerOffer(t, "gpt-4o", OfferKindPlatform, 11))
	claude := handlerMarketplaceModel("claude-3", "Claude", "Anthropic", []string{"text"}, []string{"text", "tool"},
		handlerOffer(t, "claude-3", OfferKindPrivate, 21))
	route := RoutingModel{
		ModelName: "smart-route", DisplayName: "Smart Route",
		ReachableRealModels: []string{"gpt-4o"}, FlattenedDestinations: []FlattenedDestination{},
		RoutingWarnings: []RoutingWarning{}, Guidance: RoutingModelGuidanceViewReachableRealModels,
	}
	handler := testMarketplaceHandler(t, &fakeHandlerGate{user: viewer})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{claude, gpt}}
	handler.routing = &fakeHandlerRouting{models: []RoutingModel{route}}
	handler.performance = unavailableHandlerPerformance()

	response, err := handler.List(userMarketplaceContext(t, 7), ListRequest{
		TokenID:  23,
		Search:   "GPT",
		Provider: "openai",
		Kind:     ModelKindReal,
	})

	require.NoError(t, err)
	require.Equal(t, UserSelectedTokenDTO{ID: 23, Name: "Production"}, response.SelectedToken)
	require.Equal(t, UserMarketplaceFiltersDTO{
		Providers:        []string{"Anthropic", "OpenAI"},
		InputModalities:  []string{"image", "text"},
		OutputModalities: []string{"text", "tool"},
	}, response.Filters)
	require.Len(t, response.Models, 1)
	require.Equal(t, ModelKindReal, response.Models[0].Kind)
	require.Equal(t, "gpt-4o", response.Models[0].Real.ModelName)
	require.Nil(t, response.Models[0].Routing)
	require.Equal(t, PerformanceUnavailable, response.Models[0].Real.Offers[0].PerformanceStatus)
	require.Equal(t, MarketplaceHealthUnknown, response.Models[0].Real.AggregateStatus)
}

// Break caught: accepting a window outside the three specified values or
// querying the catalog before validation would violate the HTTP contract.
func TestDetailRejectsInvalidWindowAfterGateBeforeCatalog(t *testing.T) {
	calls := []string{}
	handler := testMarketplaceHandler(t, &fakeHandlerGate{
		user:  scopedMarketplaceViewer(7, 23),
		calls: &calls,
	})
	handler.catalog = &fakeHandlerCatalog{calls: &calls}

	_, err := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{
		TokenID: 23,
		Model:   "gpt-4o",
		Window:  "1h",
	})

	requireMarketplaceAPIError(t, err, http.StatusBadRequest, "", "invalid marketplace window")
	require.Equal(t, []string{"gate:user"}, calls)
}

// Break caught: a performance cache diagnostic or usage query failure must not
// hide the model's offers, pricing, or other detail fields.
func TestDetailDegradesPerformanceAndUsageFailuresIndependently(t *testing.T) {
	viewer := scopedMarketplaceViewer(7, 23)
	viewer.Token.Name = "Production"
	offer := handlerOffer(t, "gpt-4o", OfferKindPrivate, 21)
	model := handlerMarketplaceModel("gpt-4o", "GPT-4o", "OpenAI", []string{"text"}, []string{"text"}, offer)
	handler := testMarketplaceHandler(t, &fakeHandlerGate{user: viewer})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{model}}
	handler.routing = &fakeHandlerRouting{}
	handler.performance = unavailableHandlerPerformance()
	handler.usage = &fakeHandlerUsage{err: errors.New("usage database contains secret-owner")}

	response, err := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{
		TokenID: 23,
		Model:   "gpt-4o",
		Window:  string(UsageWindow7Days),
	})

	require.NoError(t, err)
	require.Equal(t, ModelKindReal, response.Model.Kind)
	require.Equal(t, UsageUnavailable, response.UsageStatus)
	require.Len(t, response.Model.Real.Offers, 1)
	require.Equal(t, AccuracyReference, response.Model.Real.Offers[0].Pricing.Accuracy)
	require.Equal(t, PerformanceUnavailable, response.Model.Real.Offers[0].PerformanceStatus)
	require.Empty(t, response.Model.Real.Offers[0].UsageReferences)
	payload, marshalErr := json.Marshal(response)
	require.NoError(t, marshalErr)
	require.NotContains(t, string(payload), "secret-owner")
}

// Break caught: one legacy model with an invalid persisted price must not turn
// the complete catalog or an unrelated model detail into an HTTP 500, and its
// invalid price must never be serialized as a legitimate zero.
func TestHandlerSkipsLegacyInvalidPricesWithoutSynthesizingZero(t *testing.T) {
	bad := handlerMarketplaceModel(
		"legacy-bad", "Legacy bad", "Provider", nil, nil,
		handlerOffer(t, "legacy-bad", OfferKindPlatform, 11),
	)
	bad.Config.InputPrice = -1
	zero := handlerMarketplaceModel(
		"free-model", "Free model", "Provider", nil, nil,
		handlerOffer(t, "free-model", OfferKindPlatform, 12),
	)
	zero.Config.InputPrice = 0
	zero.Config.OutputPrice = 0
	zero.Config.CacheReadPrice = 0
	zero.Config.CacheWritePrice = 0
	good := handlerMarketplaceModel(
		"healthy-model", "Healthy model", "Provider", nil, nil,
		handlerOffer(t, "healthy-model", OfferKindPlatform, 13),
	)
	catalog := []MarketplaceModel{bad, zero, good}

	t.Run("ordinary catalog and unrelated detail remain available", func(t *testing.T) {
		handler := testMarketplaceHandler(t, &fakeHandlerGate{user: scopedMarketplaceViewer(7, 23)})
		handler.catalog = &fakeHandlerCatalog{models: catalog}

		list, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
		require.NoError(t, err)
		require.Len(t, list.Models, 2)
		require.Equal(t, []string{"free-model", "healthy-model"}, []string{
			list.Models[0].Real.ModelName,
			list.Models[1].Real.ModelName,
		})
		require.Zero(t, list.Models[0].Real.Offers[0].Pricing.EstimatedTotal.Input)

		payload, marshalErr := json.Marshal(list)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(payload), "legacy-bad")

		detail, detailErr := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{
			TokenID: 23, Model: "healthy-model", Window: string(UsageWindow24Hours),
		})
		require.NoError(t, detailErr)
		require.Equal(t, "healthy-model", detail.Model.Real.ModelName)
		require.Len(t, detail.Model.Real.Offers, 1)

		_, badDetailErr := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{
			TokenID: 23, Model: "legacy-bad", Window: string(UsageWindow24Hours),
		})
		requireMarketplaceAPIError(t, badDetailErr, http.StatusNotFound, "", consts.ErrNotFound)
	})

	t.Run("administrator retains model visibility without a zero-price offer", func(t *testing.T) {
		handler := testMarketplaceHandler(t, &fakeHandlerGate{admin: MarketplaceViewer{AdminGlobal: true}})
		handler.catalog = &fakeHandlerCatalog{models: catalog}

		list, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{})
		require.NoError(t, err)
		require.Len(t, list.Models, 3)
		var badModel *AdminRealModelDTO
		for index := range list.Models {
			if list.Models[index].Real != nil && list.Models[index].Real.ModelName == "legacy-bad" {
				badModel = list.Models[index].Real
				break
			}
		}
		require.NotNil(t, badModel)
		require.Empty(t, badModel.Offers)
		require.Zero(t, badModel.AvailableOfferCount)
		require.Zero(t, badModel.PlatformOfferCount)
		require.Zero(t, badModel.PrivateOfferCount)
	})
}

// Break caught: filtering invalid prices only from real-model DTOs leaves the
// same invalid offer reachable through routing summaries built from raw catalog data.
func TestHandlerRemovesLegacyInvalidPricesFromRoutingResponses(t *testing.T) {
	badOffer := handlerOffer(t, "legacy-bad", OfferKindPlatform, 11)
	bad := handlerMarketplaceModel("legacy-bad", "Legacy bad", "Provider", nil, nil, badOffer)
	bad.Config.InputPrice = -1
	goodOffer := handlerOffer(t, "healthy-model", OfferKindPlatform, 12)
	good := handlerMarketplaceModel("healthy-model", "Healthy model", "Provider", nil, nil, goodOffer)
	route := RoutingModel{
		ModelName: "smart", DisplayName: "Smart",
		ReachableRealModels: []string{"legacy-bad", "healthy-model"},
		RoutingWarnings:     []RoutingWarning{}, Guidance: RoutingModelGuidanceViewReachableRealModels,
	}
	newHandler := func(gate handlerViewerGate) *Handler {
		handler := testMarketplaceHandler(t, gate)
		handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{bad, good}}
		handler.routing = &fakeHandlerRouting{models: []RoutingModel{route}}
		return handler
	}

	t.Run("ordinary list and detail expose only priced destinations", func(t *testing.T) {
		handler := newHandler(&fakeHandlerGate{user: scopedMarketplaceViewer(7, 23)})
		list, err := handler.List(userMarketplaceContext(t, 7), ListRequest{
			TokenID: 23, Kind: ModelKindRouting,
		})
		require.NoError(t, err)
		require.Len(t, list.Models, 1)
		routing := list.Models[0].Routing
		require.Equal(t, []string{"healthy-model"}, routing.ReachableRealModels)
		require.Len(t, routing.FlattenedDestinations, 1)
		require.Equal(t, "healthy-model", routing.FlattenedDestinations[0].ModelName)
		require.Equal(t, goodOffer.OfferRef, routing.FlattenedDestinations[0].Offers[0].OfferRef)

		detail, detailErr := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{
			TokenID: 23, Model: "smart", Window: string(UsageWindow24Hours),
		})
		require.NoError(t, detailErr)
		require.Equal(t, []string{"healthy-model"}, detail.Model.Routing.ReachableRealModels)
		payload, marshalErr := json.Marshal([]any{list, detail})
		require.NoError(t, marshalErr)
		require.NotContains(t, string(payload), "legacy-bad")
		require.NotContains(t, string(payload), badOffer.OfferRef)
	})

	t.Run("administrator list and detail retain routing diagnostics without invalid offers", func(t *testing.T) {
		handler := newHandler(&fakeHandlerGate{admin: MarketplaceViewer{AdminGlobal: true}})
		list, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{Kind: ModelKindRouting})
		require.NoError(t, err)
		require.Len(t, list.Models, 1)
		routing := list.Models[0].Routing
		require.Equal(t, []string{"healthy-model"}, routing.ReachableRealModels)
		require.Len(t, routing.FlattenedDestinations, 1)
		require.Equal(t, "healthy-model", routing.FlattenedDestinations[0].ModelName)
		require.Equal(t, goodOffer.OfferRef, routing.FlattenedDestinations[0].Offers[0].OfferRef)

		detail, detailErr := handler.AdminDetail(adminMarketplaceContext(t, 1), AdminDetailRequest{
			Model: "smart", Window: string(UsageWindow24Hours),
		})
		require.NoError(t, detailErr)
		require.Equal(t, []string{"healthy-model"}, detail.Model.Routing.ReachableRealModels)
		payload, marshalErr := json.Marshal([]any{list, detail})
		require.NoError(t, marshalErr)
		require.NotContains(t, string(payload), "legacy-bad")
		require.NotContains(t, string(payload), badOffer.OfferRef)
	})
}

// Break caught: a routing-model detail accidentally acquiring the real-model
// DTO would expose price/performance/status/usage fields forbidden by the spec.
func TestDetailRoutingRootContainsOnlyRoutingGuidance(t *testing.T) {
	viewer := scopedMarketplaceViewer(7, 23)
	route := RoutingModel{
		ModelName: "smart", DisplayName: "Smart",
		ReachableRealModels: []string{"gpt-4o"},
		FlattenedDestinations: []FlattenedDestination{{
			ModelName: "gpt-4o", Offers: []ModelOfferSummary{},
		}},
		RoutingWarnings: []RoutingWarning{RoutingWarningNoVisibleOffer},
		Guidance:        RoutingModelGuidanceViewReachableRealModels,
	}
	handler := testMarketplaceHandler(t, &fakeHandlerGate{user: viewer})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{
		handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, handlerOffer(t, "gpt-4o", OfferKindPlatform, 11)),
	}}
	handler.routing = &fakeHandlerRouting{models: []RoutingModel{route}}
	handler.performance = unavailableHandlerPerformance()

	response, err := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{
		TokenID: 23,
		Model:   "smart",
		Window:  string(UsageWindow24Hours),
	})

	require.NoError(t, err)
	require.Nil(t, response.Model.Real)
	require.NotNil(t, response.Model.Routing)
	require.Equal(t, UsageNotApplicable, response.UsageStatus)
	payload, marshalErr := json.Marshal(response.Model.Routing)
	require.NoError(t, marshalErr)
	for _, forbidden := range []string{"pricing", "performance", "aggregate_status", "usage_references"} {
		require.NotContains(t, string(payload), forbidden)
	}
}

// Break caught: retaining both a real model and an enabled same-name routing
// makes list return two resources while model-only detail silently picks real.
func TestUserListAndDetailUseRuntimePriorityForSameNameEnabledRouting(t *testing.T) {
	handler, usageCalls := runtimePriorityMarketplaceHandler(
		t,
		&fakeHandlerGate{user: scopedMarketplaceViewer(7, 23)},
		true,
	)

	list, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
	require.NoError(t, err)
	require.Len(t, list.Models, 2)
	require.Equal(t, ModelKindReal, list.Models[0].Kind)
	require.Equal(t, "other", list.Models[0].Real.ModelName)
	require.Equal(t, ModelKindRouting, list.Models[1].Kind)
	require.Equal(t, "same", list.Models[1].Routing.ModelName)

	routingDetail, err := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{TokenID: 23, Model: "same"})
	require.NoError(t, err)
	require.Equal(t, ModelKindRouting, routingDetail.Model.Kind)
	require.Nil(t, routingDetail.Model.Real)
	require.Equal(t, "same", routingDetail.Model.Routing.ModelName)
	require.Equal(t, UsageNotApplicable, routingDetail.UsageStatus)
	require.Empty(t, *usageCalls, "routing detail must not query usage")

	realDetail, err := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{TokenID: 23, Model: "other"})
	require.NoError(t, err)
	require.Equal(t, ModelKindReal, realDetail.Model.Kind)
	require.Equal(t, "other", realDetail.Model.Real.ModelName)
	require.Equal(t, UsageAvailable, realDetail.UsageStatus)
	require.Equal(t, []string{"usage"}, *usageCalls)
}

// Break caught: applying collision rules only to the ordinary mapper leaves
// admin global/token-preview list and detail with a different resource arm.
func TestAdminListAndDetailUseRuntimePriorityForSameNameEnabledRouting(t *testing.T) {
	preview := scopedMarketplaceViewer(7, 23)
	for _, test := range []struct {
		name    string
		tokenID *uint
		viewer  MarketplaceViewer
	}{
		{name: "global", viewer: MarketplaceViewer{AdminGlobal: true}},
		{name: "token preview", tokenID: uintPointer(23), viewer: preview},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, usageCalls := runtimePriorityMarketplaceHandler(t, &fakeHandlerGate{admin: test.viewer}, true)

			list, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{TokenID: test.tokenID})
			require.NoError(t, err)
			require.Len(t, list.Models, 2)
			require.Equal(t, ModelKindReal, list.Models[0].Kind)
			require.Equal(t, "other", list.Models[0].Real.ModelName)
			require.Equal(t, ModelKindRouting, list.Models[1].Kind)
			require.Equal(t, "same", list.Models[1].Routing.ModelName)

			detail, err := handler.AdminDetail(adminMarketplaceContext(t, 1), AdminDetailRequest{
				TokenID: test.tokenID,
				Model:   "same",
			})
			require.NoError(t, err)
			require.Equal(t, ModelKindRouting, detail.Model.Kind)
			require.Nil(t, detail.Model.Real)
			require.Equal(t, "same", detail.Model.Routing.ModelName)
			require.Equal(t, UsageNotApplicable, detail.UsageStatus)
			require.Empty(t, *usageCalls, "routing detail must not query usage")
		})
	}
}

// Break caught: always preferring routing for a duplicate name would expose a
// disabled route that runtime skips instead of falling back to the real model.
func TestAdminGlobalListAndDetailFallBackToSameNameRealWhenRoutingDisabled(t *testing.T) {
	handler, usageCalls := runtimePriorityMarketplaceHandler(
		t,
		&fakeHandlerGate{admin: MarketplaceViewer{AdminGlobal: true}},
		false,
	)

	list, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{})
	require.NoError(t, err)
	require.Len(t, list.Models, 2)
	require.Equal(t, ModelKindReal, list.Models[0].Kind)
	require.Equal(t, "other", list.Models[0].Real.ModelName)
	require.Equal(t, ModelKindReal, list.Models[1].Kind)
	require.Equal(t, "same", list.Models[1].Real.ModelName)

	detail, err := handler.AdminDetail(adminMarketplaceContext(t, 1), AdminDetailRequest{Model: "same"})
	require.NoError(t, err)
	require.Equal(t, ModelKindReal, detail.Model.Kind)
	require.Equal(t, "same", detail.Model.Real.ModelName)
	require.Nil(t, detail.Model.Routing)
	require.Equal(t, UsageAvailable, detail.UsageStatus)
	require.Equal(t, []string{"usage"}, *usageCalls)
}

// Break caught: trusting a client offer_ref without regenerating the selected
// model's offers and checking its complete identity would expose another offer.
func TestDetailRevalidatesSubmittedOfferRefAgainstCurrentModelOffers(t *testing.T) {
	viewer := scopedMarketplaceViewer(7, 23)
	gptOffer := handlerOffer(t, "gpt-4o", OfferKindPlatform, 11)
	claudeOffer := handlerOffer(t, "claude-3", OfferKindPlatform, 11)
	handler := testMarketplaceHandler(t, &fakeHandlerGate{user: viewer})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{
		handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, gptOffer),
		handlerMarketplaceModel("claude-3", "Claude", "Anthropic", nil, nil, claudeOffer),
	}}
	handler.routing = &fakeHandlerRouting{}
	handler.performance = unavailableHandlerPerformance()

	_, err := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{
		TokenID:  23,
		Model:    "gpt-4o",
		Window:   string(UsageWindow24Hours),
		OfferRef: claudeOffer.OfferRef,
	})

	requireMarketplaceAPIError(t, err, http.StatusNotFound, "", consts.ErrNotFound)
}

// Break caught: selecting one offer while retaining the model-wide performance
// projection would report an aggregate status influenced by hidden siblings.
func TestComposedRealModelSelectOfferNarrowsPerformanceAndAggregateStatus(t *testing.T) {
	selected := handlerOffer(t, "gpt-4o", OfferKindPlatform, 11)
	sibling := handlerOffer(t, "gpt-4o", OfferKindPrivate, 21)
	model := composedRealModel{
		model: handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, selected, sibling),
		offers: []composedOffer{
			{offer: selected},
			{offer: sibling},
		},
		performance: ModelPerformanceResult{
			PerformanceStatus: PerformanceAvailable,
			ModelStatus:       MarketplaceHealthOperational,
			Offers: map[string]OfferPerformanceSnapshot{
				selected.OfferRef: {Summary: PerformanceSummary{Status: MarketplaceHealthOutage}},
				sibling.OfferRef:  {Summary: PerformanceSummary{Status: MarketplaceHealthOperational}},
			},
		},
	}

	require.True(t, model.selectOffer(selected.OfferRef, mustTestOfferRefEncoder(t)))
	require.Equal(t, map[string]OfferPerformanceSnapshot{
		selected.OfferRef: {Summary: PerformanceSummary{Status: MarketplaceHealthOutage}},
	}, model.performance.Offers)
	require.Equal(t, MarketplaceHealthOutage, mapUserRealModel(model).Real.AggregateStatus)
}

// Break caught: administrator diagnostics must retain unavailable offers, but
// their historical snapshots cannot enter current performance or aggregation.
func TestAdminListKeepsUnavailableDiagnosticsOutOfPerformanceAndAggregate(t *testing.T) {
	available := handlerOffer(t, "gpt-4o", OfferKindPlatform, 11)
	unavailable := handlerOffer(t, "gpt-4o", OfferKindPrivate, 21)
	unavailable.Available = false
	unavailable.Facts.DisabledReasons = []string{"disabled"}

	t.Run("mixed availability", func(t *testing.T) {
		model := handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, available, unavailable)
		model.AvailableOfferCount = 1
		model.PlatformOfferCount = 1
		model.PrivateOfferCount = 0
		performance := &fakeHandlerPerformance{result: ModelPerformanceResult{
			PerformanceStatus: PerformanceAvailable,
			Offers: map[string]OfferPerformanceSnapshot{
				available.OfferRef:   {Summary: PerformanceSummary{Status: MarketplaceHealthOutage}},
				unavailable.OfferRef: {Summary: PerformanceSummary{Status: MarketplaceHealthOperational}},
			},
		}}
		handler := testMarketplaceHandler(t, &fakeHandlerGate{admin: MarketplaceViewer{AdminGlobal: true}})
		handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{model}}
		handler.performance = performance

		response, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{})

		require.NoError(t, err)
		require.Equal(t, []ModelOffer{available}, performance.offers)
		require.Equal(t, MarketplaceHealthOutage, response.Models[0].Real.AggregateStatus)
		require.Len(t, response.Models[0].Real.Offers, 2)
		require.Equal(t, []string{"disabled"}, response.Models[0].Real.Offers[1].Diagnostics.DisabledReasons)
	})

	t.Run("all unavailable", func(t *testing.T) {
		model := handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, unavailable)
		model.AvailableOfferCount = 0
		handler := testMarketplaceHandler(t, &fakeHandlerGate{admin: MarketplaceViewer{AdminGlobal: true}})
		handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{model}}
		handler.performance = NewModelPerformanceFinder(nil, handler.settings)

		response, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{})

		require.NoError(t, err)
		require.Zero(t, response.Models[0].Real.AvailableOfferCount)
		require.Equal(t, MarketplaceHealthUnknown, response.Models[0].Real.AggregateStatus)
		require.Len(t, response.Models[0].Real.Offers, 1)
		assertUnknownAdminModelPerformance(
			t, response.Models[0].Real.Performance, PerformanceAvailable, UsageWindow24Hours, 24,
		)

		for _, test := range []struct {
			window      UsageWindow
			bucketCount int
		}{
			{window: UsageWindow24Hours, bucketCount: 24},
			{window: UsageWindow7Days, bucketCount: 28},
			{window: UsageWindow30Days, bucketCount: 30},
		} {
			t.Run("detail "+string(test.window), func(t *testing.T) {
				detail, detailErr := handler.AdminDetail(adminMarketplaceContext(t, 1), AdminDetailRequest{
					Model: "gpt-4o", Window: string(test.window),
				})
				require.NoError(t, detailErr)
				assertUnknownAdminModelPerformance(
					t, detail.Model.Real.Performance, PerformanceAvailable, test.window, test.bucketCount,
				)
			})
		}
	})
}

func assertUnknownAdminModelPerformance(
	t *testing.T,
	performance AdminModelPerformanceDTO,
	status PerformanceStatus,
	window UsageWindow,
	bucketCount int,
) {
	t.Helper()
	require.Equal(t, status, performance.PerformanceStatus)
	require.Equal(t, window, performance.Window)
	require.Nil(t, performance.SuccessRate)
	require.Nil(t, performance.CacheHitRate)
	require.Len(t, performance.StatusHistory, bucketCount)
	for _, bucket := range performance.StatusHistory {
		require.Equal(t, MarketplaceHealthUnknown, bucket.Status)
		require.Nil(t, bucket.SuccessRate)
	}
}

// Break caught: adding an internal domain struct to a user response would
// reintroduce one of these recursive keys or known fixture secrets.
func TestSanitizedUserDTORecursivelyForbidsInternalKeysAndSecrets(t *testing.T) {
	viewer := scopedMarketplaceViewer(7, 23)
	viewer.Token.Name = "Production"
	offer := handlerOffer(t, "gpt-4o", OfferKindPlatform, 11)
	offer.Facts = ModelOfferFacts{
		InternalName:      "platform-internal-secret",
		PublicDisplayName: "public-raw-secret",
		OwnerID:           919,
		BaseURL:           "https://internal-secret.example",
		EndpointPaths:     map[SupportedEndpoint]string{EndpointChatCompletions: "/secret/upstream/path"},
		DisabledReasons:   []string{"operator-secret-reason"},
		Billing:           OfferBilling{Identity: offer.Identity, PriceRatio: 1},
	}
	model := handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", []string{"text"}, []string{"text"}, offer)
	handler := testMarketplaceHandler(t, &fakeHandlerGate{user: viewer})
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{model}}
	handler.routing = &fakeHandlerRouting{}
	handler.performance = unavailableHandlerPerformance()

	response, err := handler.Detail(userMarketplaceContext(t, 7), DetailRequest{
		TokenID: 23, Model: "gpt-4o", Window: string(UsageWindow24Hours),
	})
	require.NoError(t, err)
	payload, marshalErr := json.Marshal(response)
	require.NoError(t, marshalErr)

	var decoded any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	assertForbiddenJSONKeys(t, decoded, map[string]struct{}{
		"channel_id": {}, "private_channel_id": {}, "internal_name": {}, "channel_type": {},
		"public_display_name": {}, "endpoint_paths": {}, "base_url": {}, "key": {}, "key_cipher": {},
		"owner_id": {}, "priority": {}, "weight": {}, "rule_id": {}, "request_count": {},
		"success_count": {}, "failure_count": {}, "sample_count": {}, "identity": {}, "source_id": {},
	})
	for _, secret := range []string{
		"platform-internal-secret", "public-raw-secret", "internal-secret.example",
		"/secret/upstream/path", "operator-secret-reason",
	} {
		require.NotContains(t, string(payload), secret)
	}
	selectedTokenJSON, marshalErr := json.Marshal(response.SelectedToken)
	require.NoError(t, marshalErr)
	var selectedToken map[string]any
	require.NoError(t, json.Unmarshal(selectedTokenJSON, &selectedToken))
	require.Equal(t, []string{"id", "name"}, sortedMapKeys(selectedToken))
}

// Break caught: reusing the ordinary mapper for admin output would either hide
// diagnostics or leak credentials when the internal channel struct grows.
func TestAdminGlobalAndTokenPreviewUseIndependentDiagnosticDTOWithoutCredentials(t *testing.T) {
	global := MarketplaceViewer{AdminGlobal: true}
	preview := scopedMarketplaceViewer(7, 23)
	preview.Token.Name = "Preview"
	offer := handlerOffer(t, "gpt-4o", OfferKindPrivate, 21)
	offer.Facts = ModelOfferFacts{
		InternalName:    "private-internal",
		OwnerID:         7,
		BaseURL:         "https://byok-admin.example",
		EndpointPaths:   map[SupportedEndpoint]string{EndpointMessages: "/v1/messages"},
		DisabledReasons: []string{"disabled"},
		Billing:         OfferBilling{Identity: offer.Identity},
	}
	offer.Available = false
	model := handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, offer)

	for _, test := range []struct {
		name      string
		tokenID   *uint
		viewer    MarketplaceViewer
		wantMode  AdminMarketplaceMode
		wantToken *AdminSelectedTokenDTO
	}{
		{name: "global", viewer: global, wantMode: AdminModeGlobal},
		{name: "token preview", tokenID: uintPointer(23), viewer: preview, wantMode: AdminModeTokenPreview,
			wantToken: &AdminSelectedTokenDTO{ID: 23, Name: "Preview"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := testMarketplaceHandler(t, &fakeHandlerGate{admin: test.viewer})
			handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{model}}
			handler.routing = &fakeHandlerRouting{}
			handler.performance = unavailableHandlerPerformance()

			response, err := handler.AdminList(adminMarketplaceContext(t, 1), AdminListRequest{TokenID: test.tokenID})
			require.NoError(t, err)
			require.Equal(t, test.wantMode, response.View.Mode)
			require.Equal(t, test.wantToken, response.View.SelectedToken)
			diagnostic := response.Models[0].Real.Offers[0].Diagnostics
			require.Equal(t, uint(21), diagnostic.PrivateChannelID)
			require.Equal(t, "private-internal", diagnostic.InternalName)
			require.Equal(t, uint(7), diagnostic.OwnerID)
			require.Equal(t, "https://byok-admin.example", diagnostic.BaseURL)
			require.Equal(t, []AdminEndpointPathDTO{{Endpoint: EndpointMessages, Path: "/v1/messages"}}, diagnostic.EndpointPaths)

			payload, marshalErr := json.Marshal(response)
			require.NoError(t, marshalErr)
			require.NotContains(t, string(payload), `"channel_type"`)
			for _, credential := range []string{"api-key-secret", "cipher-secret", "key_cipher", "key_last4"} {
				require.NotContains(t, string(payload), credential)
			}
		})
	}
}

func TestMarketplaceDTOEmptyCollectionsMarshalAsArrays(t *testing.T) {
	offer := handlerOffer(t, "gpt-4o", OfferKindPlatform, 11)
	offer.SupportedEndpoints = []SupportedEndpoint{}
	offer.Facts.DisabledReasons = []string{}
	model := composedRealModel{
		model:  handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", []string{}, []string{}, offer),
		offers: []composedOffer{{offer: offer}},
		performance: ModelPerformanceResult{
			PerformanceStatus: PerformanceAvailable,
			Offers:            map[string]OfferPerformanceSnapshot{},
		},
	}

	payload, err := json.Marshal(mapAdminRealModel(model))
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(payload, &decoded))
	real := decoded["real"].(map[string]any)
	metadata := real["metadata"].(map[string]any)
	firstOffer := real["offers"].([]any)[0].(map[string]any)
	diagnostics := firstOffer["diagnostics"].(map[string]any)

	require.Equal(t, []any{}, metadata["input_modalities"])
	require.Equal(t, []any{}, metadata["output_modalities"])
	require.Equal(t, []any{}, firstOffer["supported_endpoints"])
	require.Equal(t, []any{}, diagnostics["disabled_reasons"])
}

// Break caught: reusing the ordinary performance projection without its
// internal counters would hide the sample facts administrators need to
// diagnose unknown status, while serializing them directly would leak them.
func TestAdminPerformanceDTOIncludesSampleDiagnosticsThatUserDTOCannotSerialize(t *testing.T) {
	offer := handlerOffer(t, "gpt-4o", OfferKindPlatform, 11)
	model := composedRealModel{
		model:  handlerMarketplaceModel("gpt-4o", "GPT", "OpenAI", nil, nil, offer),
		offers: []composedOffer{{offer: offer}},
		performance: ModelPerformanceResult{
			PerformanceStatus: PerformanceAvailable,
			ModelStatus:       MarketplaceHealthUnknown,
			Offers: map[string]OfferPerformanceSnapshot{
				offer.OfferRef: {
					Summary: PerformanceSummary{Status: MarketplaceHealthUnknown},
					Diagnostics: PerformanceDiagnostics{
						RequestCount: 19, SuccessCount: 18, FailureCount: 1, StreamRequestCount: 17,
						TTFTSampleCount: 16, TPSSampleCount: 15, DurationSampleCount: 14,
					},
				},
			},
		},
	}

	admin := mapAdminRealModel(model)
	require.Equal(t, MarketplaceHealthUnknown, admin.Real.Offers[0].Performance.Status)
	require.Equal(t, int64(19), admin.Real.Offers[0].Performance.RequestCount)
	require.Equal(t, int64(18), admin.Real.Offers[0].Performance.SuccessCount)
	require.Equal(t, int64(1), admin.Real.Offers[0].Performance.FailureCount)
	require.Equal(t, int64(17), admin.Real.Offers[0].Performance.StreamRequestCount)
	require.Equal(t, int64(16), admin.Real.Offers[0].Performance.TTFTSampleCount)
	require.Equal(t, int64(15), admin.Real.Offers[0].Performance.TPSSampleCount)
	require.Equal(t, int64(14), admin.Real.Offers[0].Performance.DurationSampleCount)

	userPayload, err := json.Marshal(mapUserRealModel(model))
	require.NoError(t, err)
	for _, forbidden := range []string{
		"request_count", "success_count", "failure_count", "stream_request_count",
		"ttft_sample_count", "tps_sample_count", "duration_sample_count",
	} {
		require.NotContains(t, string(userPayload), forbidden)
	}
}

// Break caught: dropping internal routing facts after the batched walk would
// leave administrators without the complete root-to-global rule chain, while
// putting those facts on the user mapper would leak priority and weight.
func TestAdminRoutingDTOIncludesRuleChainThatUserDTOCannotSerialize(t *testing.T) {
	viewer := scopedMarketplaceViewer(7, 23)
	query := &fakeRoutingModelQuery{routings: []models.ModelRouting{
		routingRow(10, "smart", models.RoutingScopeToken, 0, 23, true,
			routingMembers(member("pool", 10, 3))),
		routingRow(20, "pool", models.RoutingScopeGlobal, 0, 0, true,
			routingMembers(member("gpt-4o", 5, 1))),
	}}
	routes, err := NewRoutingModelFinder(query).Find(t.Context(), viewer, routingOffers("gpt-4o"))
	require.NoError(t, err)
	route := findRoutingModel(t, routes, "smart")

	admin := mapAdminRoutingModel(route)
	require.Equal(t, []AdminRoutingDefinitionDTO{
		{
			OccurrenceID: "root:10",
			Path:         []AdminRoutingPathDTO{{Ref: "smart", RoutingID: 10}},
			RoutingID:    10, Name: "smart", Scope: models.RoutingScopeToken, TokenID: 23, Enabled: true,
			Members: []AdminRoutingMemberDTO{{
				Ref: "pool", Priority: 10, Weight: 3,
				Kind: RoutingMemberTargetRouting, RoutingID: 20,
			}},
		},
		{
			OccurrenceID: "root:10/0:20",
			Path: []AdminRoutingPathDTO{
				{Ref: "smart", RoutingID: 10},
				{Ref: "pool", RoutingID: 20},
			},
			RoutingID: 20, Name: "pool", Scope: models.RoutingScopeGlobal, Enabled: true,
			Members: []AdminRoutingMemberDTO{{
				Ref: "gpt-4o", Priority: 5, Weight: 1,
				Kind: RoutingMemberTargetModel, ModelName: "gpt-4o",
			}},
		},
	}, admin.Routing.Diagnostics.Definitions)
	routingMemberJSON, marshalErr := json.Marshal(admin.Routing.Diagnostics.Definitions[0].Members[0])
	require.NoError(t, marshalErr)
	require.JSONEq(t, `{
		"ref":"pool","priority":10,"weight":3,"kind":"routing","routing_id":20
	}`, string(routingMemberJSON))
	modelMemberJSON, marshalErr := json.Marshal(admin.Routing.Diagnostics.Definitions[1].Members[0])
	require.NoError(t, marshalErr)
	require.JSONEq(t, `{
		"ref":"gpt-4o","priority":5,"weight":1,"kind":"model","model_name":"gpt-4o"
	}`, string(modelMemberJSON))
	adminPayload, marshalErr := json.Marshal(admin)
	require.NoError(t, marshalErr)
	var routingJSON struct {
		Routing struct {
			Diagnostics struct {
				Definitions []map[string]any `json:"definitions"`
			} `json:"diagnostics"`
		} `json:"routing"`
	}
	require.NoError(t, json.Unmarshal(adminPayload, &routingJSON))
	require.Equal(t, []string{
		"enabled", "members", "name", "occurrence_id", "path", "routing_id", "scope", "token_id", "user_id",
	}, sortedJSONKeys(routingJSON.Routing.Diagnostics.Definitions[0]))
	path := routingJSON.Routing.Diagnostics.Definitions[0]["path"].([]any)
	require.Equal(t, []string{"ref", "routing_id"}, sortedJSONKeys(path[0].(map[string]any)))
	require.NotEmpty(t, routingJSON.Routing.Diagnostics.Definitions[0]["occurrence_id"])
	for _, forbidden := range []string{"key", "cipher", "credential"} {
		require.NotContains(t, strings.ToLower(string(adminPayload)), forbidden)
	}

	userPayload, marshalErr := json.Marshal(mapUserRoutingModel(route))
	require.NoError(t, marshalErr)
	for _, forbidden := range []string{
		"diagnostics", "occurrence_id", "path", "routing_id", "priority", "weight", "token_id", "user_id",
	} {
		require.NotContains(t, string(userPayload), forbidden)
	}
}

// Break caught: adding a query inside any model/offer loop changes these fixed
// counts when the fixture grows. The second call also proves hot-cache behavior.
func TestListUsesFixedBatchQueriesForMultipleModelsAndOffersOnColdAndHotCache(t *testing.T) {
	query := &countingHandlerQuery{
		configs: []models.ModelConfig{
			handlerModelConfig("claude-3", "Claude", "Anthropic", 1, 2, 3, 4),
			handlerModelConfig("gpt-4o", "GPT", "OpenAI", 2, 4, 6, 8),
		},
		channels: []models.Channel{
			handlerPlatformChannel(1, "one", "One", "gpt-4o,claude-3", 1),
			handlerPlatformChannel(2, "two", "Two", "gpt-4o,claude-3", 0.8),
		},
		private: []models.PrivateChannel{
			marketplacePrivateChannel(11, 7, "Mine", "gpt-4o", `{"messages":"/messages"}`),
			marketplacePrivateChannel(12, 7, "Mine Claude", "claude-3", `{"messages":"/messages"}`),
		},
	}
	usage := &fakeMarketplaceUsageQuery{}
	now := time.Date(2026, 7, 31, 12, 30, 0, 0, time.UTC)
	loads := 0
	cacheReads := 0
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(context.Context, time.Time) (*GlobalModelPerformanceSnapshot, error) {
		loads++
		return &GlobalModelPerformanceSnapshot{ObservedUntil: now, Offers: map[ModelOfferIdentity]GlobalOfferPerformanceSnapshot{}}, nil
	}))
	cache.afterStateLoad = func() { cacheReads++ }
	settings := app.NewMasterSettingsSnapshot()
	settings.Replace(map[string]string{
		consts.SettingKeyBYOKBillingMode:     consts.BYOKBillingModeServiceFee,
		consts.SettingKeyBYOKServiceFeeRatio: "0.1",
	})
	handler, err := NewHandler(query, usage, cache, settings, "handler-query-count-secret-at-least-32-bytes")
	require.NoError(t, err)
	handler.clock = func() time.Time { return now }
	handler.gate = &fakeHandlerGate{user: scopedMarketplaceViewer(7, 23)}
	handler.SetModelOfferPlanFinder(&fakeHandlerOfferPlanFinder{plans: map[string]relayplan.ModelOfferPlan{
		"claude-3": offerPlan("claude-3",
			offerPlanCandidate("claude-3", state.SourceAdmin, 1),
			offerPlanCandidate("claude-3", state.SourceAdmin, 2),
			offerPlanCandidate("claude-3", state.SourcePrivate, 12),
		),
		"gpt-4o": offerPlan("gpt-4o",
			offerPlanCandidate("gpt-4o", state.SourceAdmin, 1),
			offerPlanCandidate("gpt-4o", state.SourceAdmin, 2),
			offerPlanCandidate("gpt-4o", state.SourcePrivate, 11),
		),
	}})

	first, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
	require.NoError(t, err)
	require.Len(t, first.Models, 2)
	require.Equal(t, handlerQueryCounts{models: 1, channels: 1, private: 1, routings: 1}, query.counts)
	require.Equal(t, 1, loads, "cold list must load the process cache once, not once per model or offer")
	require.Equal(t, 1, cacheReads, "one request must read and project the process cache in one batch")
	require.Zero(t, usage.selectedCalls+usage.ownerCalls+usage.adminCalls, "directory must never query private usage")

	second, err := handler.List(userMarketplaceContext(t, 7), ListRequest{TokenID: 23})
	require.NoError(t, err)
	require.Len(t, second.Models, 2)
	require.Equal(t, handlerQueryCounts{models: 2, channels: 2, private: 2, routings: 2}, query.counts)
	require.Equal(t, 1, loads, "hot list must reuse the same cache snapshot")
	require.Equal(t, 2, cacheReads, "the second request adds exactly one cache read")
	require.Same(t, cache, handler.PerformanceCache())
}

func testMarketplaceHandler(t *testing.T, gate handlerViewerGate) *Handler {
	t.Helper()
	settings := app.NewMasterSettingsSnapshot()
	settings.Replace(map[string]string{
		consts.SettingKeyBYOKBillingMode:     consts.BYOKBillingModeFree,
		consts.SettingKeyBYOKServiceFeeRatio: consts.BYOKDefaultServiceFeeRatioStr,
	})
	handler := &Handler{
		gate:        gate,
		pricing:     ModelPriceFinder{},
		encoder:     mustTestOfferRefEncoder(t),
		settings:    settings,
		clock:       func() time.Time { return time.Date(2026, 7, 31, 12, 30, 0, 0, time.UTC) },
		catalog:     &fakeHandlerCatalog{},
		routing:     &fakeHandlerRouting{},
		performance: unavailableHandlerPerformance(),
		usage:       &fakeHandlerUsage{references: map[string][]OfferUsageReference{}},
	}
	handler.SetModelOfferPlanFinder(&mirroredHandlerOfferPlanFinder{handler: handler})
	return handler
}

func runtimePriorityMarketplaceHandler(t *testing.T, gate handlerViewerGate, routingEnabled bool) (*Handler, *[]string) {
	t.Helper()
	same := handlerMarketplaceModel(
		"same", "Same Real", "Provider", nil, nil,
		handlerOffer(t, "same", OfferKindPlatform, 11),
	)
	other := handlerMarketplaceModel(
		"other", "Other Real", "Provider", nil, nil,
		handlerOffer(t, "other", OfferKindPlatform, 12),
	)
	warnings := []RoutingWarning{}
	if !routingEnabled {
		warnings = []RoutingWarning{RoutingWarningDisabled}
	}
	routing := RoutingModel{
		ModelName: "same", DisplayName: "Same Routing",
		ReachableRealModels: []string{"same"}, FlattenedDestinations: []FlattenedDestination{},
		RoutingWarnings: warnings, Guidance: RoutingModelGuidanceViewReachableRealModels,
		Facts: RoutingModelFacts{Definitions: []RoutingDefinitionFact{{
			RoutingID: 31, Name: "same", Scope: models.RoutingScopeGlobal, Enabled: routingEnabled,
		}}},
	}
	usageCalls := &[]string{}
	handler := testMarketplaceHandler(t, gate)
	handler.catalog = &fakeHandlerCatalog{models: []MarketplaceModel{same, other}}
	handler.routing = &fakeHandlerRouting{models: []RoutingModel{routing}}
	handler.performance = unavailableHandlerPerformance()
	handler.usage = &fakeHandlerUsage{references: map[string][]OfferUsageReference{}, calls: usageCalls}
	return handler, usageCalls
}

func handlerMarketplaceModel(
	modelName, displayName, provider string,
	inputs, outputs []string,
	offers ...ModelOffer,
) MarketplaceModel {
	return MarketplaceModel{
		ModelName: modelName,
		Metadata: models.ModelMetadata{
			DisplayName: displayName, Provider: provider,
			InputModalities: inputs, OutputModalities: outputs,
		},
		Config: models.ModelConfig{
			ModelName: modelName, InputPrice: 1, OutputPrice: 2, CacheReadPrice: 3, CacheWritePrice: 4,
		},
		AvailableOfferCount: len(offers), Offers: offers,
	}
}

func handlerOffer(t *testing.T, modelName string, kind ModelOfferKind, sourceID uint) ModelOffer {
	t.Helper()
	identity := ModelOfferIdentity{ModelName: modelName, Kind: kind, SourceID: sourceID}
	ref, err := mustTestOfferRefEncoder(t).Encode(identity)
	require.NoError(t, err)
	ownership := OfferPlatform
	billing := OfferBilling{Identity: identity, PriceRatio: 1}
	if kind == OfferKindPrivate {
		ownership = OfferOwned
		billing = OfferBilling{Identity: identity}
	}
	return ModelOffer{
		OfferRef: ref, Kind: kind, DisplayName: "Visible", Ownership: ownership, Available: true,
		SupportedEndpoints: []SupportedEndpoint{EndpointChatCompletions}, Identity: identity,
		Facts: ModelOfferFacts{
			InternalName: "internal", EndpointPaths: map[SupportedEndpoint]string{EndpointChatCompletions: "/chat"},
			Billing: billing,
		},
	}
}

func unavailableHandlerPerformance() *fakeHandlerPerformance {
	return &fakeHandlerPerformance{
		result: ModelPerformanceResult{
			PerformanceStatus: PerformanceUnavailable,
			ModelStatus:       MarketplaceHealthUnknown,
			Offers:            map[string]OfferPerformanceSnapshot{},
		},
		err: errors.New("performance cache unavailable"),
	}
}

type fakeHandlerGate struct {
	user     MarketplaceViewer
	admin    MarketplaceViewer
	userErr  error
	adminErr error
	calls    *[]string
}

func (f *fakeHandlerGate) RequireUser(*app.Context, uint) (MarketplaceViewer, error) {
	appendHandlerCall(f.calls, "gate:user")
	return f.user, f.userErr
}

func (f *fakeHandlerGate) RequireAdmin(*app.Context, *uint) (MarketplaceViewer, error) {
	appendHandlerCall(f.calls, "gate:admin")
	return f.admin, f.adminErr
}

type fakeHandlerCatalog struct {
	models []MarketplaceModel
	err    error
	calls  *[]string
}

func (f *fakeHandlerCatalog) Find(context.Context, MarketplaceViewer) ([]MarketplaceModel, error) {
	appendHandlerCall(f.calls, "catalog")
	return append([]MarketplaceModel(nil), f.models...), f.err
}

type fakeHandlerRouting struct {
	models []RoutingModel
	err    error
	calls  *[]string
}

type fakeHandlerOfferPlanFinder struct {
	plans   map[string]relayplan.ModelOfferPlan
	errors  map[string]error
	queries []relayplan.ModelOfferPlanQuery
}

func (f *fakeHandlerOfferPlanFinder) Find(
	_ context.Context,
	query relayplan.ModelOfferPlanQuery,
) (relayplan.ModelOfferPlan, error) {
	f.queries = append(f.queries, query)
	if err := f.errors[query.Model]; err != nil {
		return relayplan.ModelOfferPlan{RequestModel: query.Model}, err
	}
	result, ok := f.plans[query.Model]
	if !ok {
		return relayplan.ModelOfferPlan{RequestModel: query.Model}, nil
	}
	return result, nil
}

type mirroredHandlerOfferPlanFinder struct {
	handler *Handler
}

func (f *mirroredHandlerOfferPlanFinder) Find(
	_ context.Context,
	query relayplan.ModelOfferPlanQuery,
) (relayplan.ModelOfferPlan, error) {
	result := relayplan.ModelOfferPlan{RequestModel: query.Model}
	if f == nil || f.handler == nil {
		return result, relayplan.ErrModelOfferPlanUnavailable
	}
	catalog, _ := f.handler.catalog.(*fakeHandlerCatalog)
	routing, _ := f.handler.routing.(*fakeHandlerRouting)
	if route, ok := fakeRoutingModel(routing, query.Model); ok && fakeRoutingEnabled(route) {
		result.Routing = true
		result.RealModels = append([]string(nil), route.ReachableRealModels...)
		for _, realModel := range route.ReachableRealModels {
			result.Candidates = append(result.Candidates, fakeCatalogPlanCandidates(catalog, realModel)...)
		}
		return result, nil
	}
	result.RealModels = []string{query.Model}
	result.Candidates = fakeCatalogPlanCandidates(catalog, query.Model)
	return result, nil
}

func fakeRoutingModel(routing *fakeHandlerRouting, model string) (RoutingModel, bool) {
	if routing == nil {
		return RoutingModel{}, false
	}
	for _, candidate := range routing.models {
		if candidate.ModelName == model {
			return candidate, true
		}
	}
	return RoutingModel{}, false
}

func fakeRoutingEnabled(model RoutingModel) bool {
	if len(model.Facts.Definitions) == 0 {
		return true
	}
	return model.Facts.Definitions[0].Enabled
}

func fakeCatalogPlanCandidates(catalog *fakeHandlerCatalog, model string) []relayplan.ModelOfferCandidate {
	if catalog == nil {
		return nil
	}
	var result []relayplan.ModelOfferCandidate
	for _, candidate := range catalog.models {
		if candidate.ModelName != model {
			continue
		}
		for _, offer := range candidate.Offers {
			if !offer.Available {
				continue
			}
			source := state.SourceAdmin
			if offer.Kind == OfferKindPrivate {
				source = state.SourcePrivate
			}
			result = append(result, offerPlanCandidate(model, source, offer.Identity.SourceID))
		}
	}
	return result
}

func offerPlan(model string, candidates ...relayplan.ModelOfferCandidate) relayplan.ModelOfferPlan {
	return relayplan.ModelOfferPlan{
		RequestModel: model, RealModels: []string{model}, Candidates: candidates,
	}
}

func offerPlanCandidate(
	model string,
	source state.ChannelSource,
	sourceID uint,
) relayplan.ModelOfferCandidate {
	return relayplan.ModelOfferCandidate{
		RealModel: model, Source: source, SourceID: sourceID, Modes: handlerOfferPlanModes(),
	}
}

func handlerOfferPlanModes() []relayplan.ModelOfferCandidateMode {
	return []relayplan.ModelOfferCandidateMode{
		{Protocol: codec.ProtocolOpenAIChat, Mode: state.ModeNative},
		{Protocol: codec.ProtocolOpenAIResponses, Mode: state.ModeNative},
		{Protocol: codec.ProtocolClaude, Mode: state.ModeNative},
	}
}

func (f *fakeHandlerRouting) Find(context.Context, MarketplaceViewer, map[string][]ModelOffer) ([]RoutingModel, error) {
	appendHandlerCall(f.calls, "routing")
	return append([]RoutingModel(nil), f.models...), f.err
}

type fakeHandlerPerformance struct {
	result ModelPerformanceResult
	err    error
	calls  *[]string
	offers []ModelOffer
}

func (f *fakeHandlerPerformance) Find(_ context.Context, offers []ModelOffer, _ UsageWindow, _ time.Time) (ModelPerformanceResult, error) {
	appendHandlerCall(f.calls, "performance")
	f.offers = append([]ModelOffer(nil), offers...)
	return f.result, f.err
}

type fakeHandlerUsage struct {
	references map[string][]OfferUsageReference
	err        error
	calls      *[]string
}

func (f *fakeHandlerUsage) Find(context.Context, MarketplaceViewer, []ModelOffer, UsageWindow) (map[string][]OfferUsageReference, error) {
	appendHandlerCall(f.calls, "usage")
	return f.references, f.err
}

func appendHandlerCall(calls *[]string, call string) {
	if calls != nil {
		*calls = append(*calls, call)
	}
}

func assertForbiddenJSONKeys(t *testing.T, value any, forbidden map[string]struct{}) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, denied := forbidden[key]; denied {
				t.Errorf("forbidden JSON key %q is present", key)
			}
			assertForbiddenJSONKeys(t, child, forbidden)
		}
	case []any:
		for _, child := range typed {
			assertForbiddenJSONKeys(t, child, forbidden)
		}
	}
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func uintPointer(value uint) *uint { return &value }

type handlerQueryCounts struct {
	models   int
	channels int
	private  int
	routings int
}

type countingHandlerQuery struct {
	configs  []models.ModelConfig
	channels []models.Channel
	private  []models.PrivateChannel
	routings []models.ModelRouting
	counts   handlerQueryCounts
}

func (q *countingHandlerQuery) ListEnabledMarketplaceModels(context.Context) ([]models.ModelConfig, error) {
	q.counts.models++
	return append([]models.ModelConfig(nil), q.configs...), nil
}

func (q *countingHandlerQuery) ListMarketplaceChannels(context.Context) ([]models.Channel, error) {
	q.counts.channels++
	return append([]models.Channel(nil), q.channels...), nil
}

func (q *countingHandlerQuery) ListMarketplacePrivateChannels(context.Context, dao.MarketplacePrivateChannelScope) ([]models.PrivateChannel, error) {
	q.counts.private++
	return append([]models.PrivateChannel(nil), q.private...), nil
}

func (q *countingHandlerQuery) ListMarketplaceRoutings(context.Context, dao.MarketplaceRoutingScope) ([]models.ModelRouting, error) {
	q.counts.routings++
	return append([]models.ModelRouting(nil), q.routings...), nil
}

func handlerModelConfig(modelName, displayName, provider string, input, output, cacheRead, cacheWrite float64) models.ModelConfig {
	return models.ModelConfig{
		ModelName: modelName, Status: consts.StatusEnabled,
		InputPrice: input, OutputPrice: output, CacheReadPrice: cacheRead, CacheWritePrice: cacheWrite,
		SyncedMetadata: datatypes.NewJSONType(models.ModelMetadata{
			DisplayName: displayName, Provider: provider,
			InputModalities: []string{"text"}, OutputModalities: []string{"text"},
		}),
	}
}

func handlerPlatformChannel(id uint, internalName, publicName, modelNames string, ratio float64) models.Channel {
	return models.Channel{
		ChannelCore: models.ChannelCore{
			ID: id, Name: internalName, Status: consts.StatusEnabled,
			BaseURL: "https://internal.example", Endpoints: `{"chat_completions":"/v1/chat/completions"}`,
		},
		PublicDisplayName: publicName, Models: modelNames, PriceRatio: ratio, Key: "api-key-secret",
	}
}

var _ dao.ModelMarketplaceQuery = (*countingHandlerQuery)(nil)
