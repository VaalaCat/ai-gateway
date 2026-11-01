package model_marketplace

import (
	"testing"

	relayplan "github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/plan"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/stretchr/testify/require"
)

func TestRoutingOfferWarningFollowsRuntimePlanAndPricing(t *testing.T) {
	offer := handlerOffer(t, "model-a", OfferKindPlatform, 11)
	// The catalog walk saw an unavailable offer, but runtime planning can use it.
	offer.Available = false
	route := RoutingModel{
		ModelName:       "auto",
		RoutingWarnings: []RoutingWarning{RoutingWarningCycle, RoutingWarningNoVisibleOffer},
		Facts: RoutingModelFacts{Definitions: []RoutingDefinitionFact{{
			Enabled: true,
			Members: []RoutingMemberTargetFact{{Kind: RoutingMemberTargetModel, ModelName: "model-a"}},
		}}},
	}
	plan := offerPlan("auto", offerPlanCandidate("model-a", state.SourceAdmin, 11))
	plan.Routing = true
	plans := marketplaceOfferPlans{byRequestModel: map[string]relayplan.ModelOfferPlan{"auto": plan}}
	catalog := []MarketplaceModel{handlerMarketplaceModel("model-a", "A", "Provider", nil, nil, offer)}
	routes, _ := applyOfferPlansToRouting([]RoutingModel{route}, plans, catalog, true)
	require.Equal(t, []string{"model-a"}, routes[0].ReachableRealModels)
	require.Equal(t, []RoutingWarning{RoutingWarningCycle}, routes[0].RoutingWarnings)

	t.Run("priced offer remains visible", func(t *testing.T) {
		result := filterRoutingModelsToPricedOffers(routes, map[string]struct{}{offer.OfferRef: {}}, true)
		require.NotContains(t, result[0].RoutingWarnings, RoutingWarningNoVisibleOffer)
	})
	t.Run("pricing hides offer", func(t *testing.T) {
		result := filterRoutingModelsToPricedOffers(routes, nil, true)
		require.Contains(t, result[0].RoutingWarnings, RoutingWarningNoVisibleOffer)
	})
	t.Run("another target has no offer", func(t *testing.T) {
		route.Facts.Definitions[0].Members = append(route.Facts.Definitions[0].Members,
			RoutingMemberTargetFact{Kind: RoutingMemberTargetModel, ModelName: "model-b"})
		result, _ := applyOfferPlansToRouting([]RoutingModel{route}, plans, catalog, true)
		require.Equal(t, []string{"model-a"}, result[0].ReachableRealModels)
		require.Contains(t, result[0].RoutingWarnings, RoutingWarningNoVisibleOffer)
	})
}
