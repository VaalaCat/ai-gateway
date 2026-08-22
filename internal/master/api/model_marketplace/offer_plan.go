package model_marketplace

import (
	"context"
	"sort"

	relayplan "github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/plan"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

var marketplaceInboundProtocols = []llmkit.Protocol{
	llmkit.ProtocolOpenAIChat,
	llmkit.ProtocolOpenAIResponses,
	llmkit.ProtocolClaude,
}

type ModelOfferPlanFinder interface {
	Find(context.Context, relayplan.ModelOfferPlanQuery) (relayplan.ModelOfferPlan, error)
}

type modelOfferPlanFinderSnapshot struct {
	finder ModelOfferPlanFinder
}

type marketplaceOfferPlans struct {
	byRequestModel map[string]relayplan.ModelOfferPlan
}

func (h *Handler) SetModelOfferPlanFinder(finder ModelOfferPlanFinder) {
	if h == nil {
		return
	}
	if finder == nil {
		h.offerPlanFinder.Store(nil)
		return
	}
	h.offerPlanFinder.Store(&modelOfferPlanFinderSnapshot{finder: finder})
}

func (h *Handler) ModelOfferPlanningReady() bool {
	return h != nil && h.modelOfferPlanFinder() != nil
}

func (h *Handler) modelOfferPlanFinder() ModelOfferPlanFinder {
	if h == nil {
		return nil
	}
	snapshot := h.offerPlanFinder.Load()
	if snapshot == nil {
		return nil
	}
	return snapshot.finder
}

func findMarketplaceOfferPlans(
	ctx context.Context,
	finder ModelOfferPlanFinder,
	viewer MarketplaceViewer,
	modelNames []string,
) marketplaceOfferPlans {
	result := marketplaceOfferPlans{byRequestModel: make(map[string]relayplan.ModelOfferPlan)}
	if finder == nil {
		return result
	}
	userInfo := marketplaceRelayUserInfo(viewer)
	for _, modelName := range sortedUniqueMarketplaceNames(modelNames) {
		plan, err := finder.Find(ctx, relayplan.ModelOfferPlanQuery{
			Model:            modelName,
			UserInfo:         userInfo,
			InboundProtocols: append([]llmkit.Protocol(nil), marketplaceInboundProtocols...),
		})
		if err != nil || plan.RequestModel != modelName {
			continue
		}
		result.byRequestModel[modelName] = plan
	}
	return result
}

func marketplaceRelayUserInfo(viewer MarketplaceViewer) *app.UserInfo {
	if viewer.AdminGlobal {
		return nil
	}
	result := &app.UserInfo{
		UserID:                 viewer.UserID,
		BYOKOnly:               viewer.BYOKOnly,
		AllowedChannelIDs:      append([]uint(nil), viewer.TokenAllowedChannelIDs...),
		GroupAllowedChannelIDs: append([]uint(nil), viewer.GroupAllowedChannelIDs...),
		TokenModels:            append([]string(nil), viewer.AllowedModels.TokenPatterns...),
		GroupModels:            append([]string(nil), viewer.AllowedModels.GroupPatterns...),
		GroupID:                viewer.GroupID,
	}
	if viewer.Token != nil {
		result.TokenID = viewer.Token.ID
		result.TokenName = viewer.Token.Name
	}
	return result
}

func applyOfferPlansToCatalog(
	catalog []MarketplaceModel,
	plans marketplaceOfferPlans,
	retainDiagnostics bool,
) []MarketplaceModel {
	result := make([]MarketplaceModel, 0, len(catalog))
	for _, model := range catalog {
		plan, known := plans.byRequestModel[model.ModelName]
		candidates := offerPlanCandidatesByIdentity(plan, known, model.ModelName)
		model.AvailableOfferCount = 0
		model.PlatformOfferCount = 0
		model.PrivateOfferCount = 0
		offers := make([]ModelOffer, 0, len(model.Offers))
		for _, offer := range model.Offers {
			candidate, available := candidates[offer.Identity]
			offer.Available = available
			if available {
				offer.SupportedEndpoints = plannedSupportedEndpoints(offer.SupportedEndpoints, candidate.Modes)
				model.AvailableOfferCount++
				if offer.Kind == OfferKindPlatform {
					model.PlatformOfferCount++
				} else if offer.Kind == OfferKindPrivate {
					model.PrivateOfferCount++
				}
			}
			if retainDiagnostics || available {
				offers = append(offers, offer)
			}
		}
		model.Offers = offers
		if retainDiagnostics || model.AvailableOfferCount > 0 {
			result = append(result, model)
		}
	}
	return result
}

func offerPlanCandidatesByIdentity(
	plan relayplan.ModelOfferPlan,
	known bool,
	realModel string,
) map[ModelOfferIdentity]relayplan.ModelOfferCandidate {
	result := make(map[ModelOfferIdentity]relayplan.ModelOfferCandidate)
	if !known {
		return result
	}
	for _, candidate := range plan.Candidates {
		if candidate.RealModel != realModel || candidate.SourceID == 0 {
			continue
		}
		identity, ok := modelOfferIdentityFromCandidate(candidate)
		if !ok {
			continue
		}
		result[identity] = candidate
	}
	return result
}

func modelOfferIdentityFromCandidate(candidate relayplan.ModelOfferCandidate) (ModelOfferIdentity, bool) {
	kind := OfferKindPlatform
	switch candidate.Source {
	case state.SourceAdmin:
	case state.SourcePrivate:
		kind = OfferKindPrivate
	default:
		return ModelOfferIdentity{}, false
	}
	if candidate.RealModel == "" || candidate.SourceID == 0 {
		return ModelOfferIdentity{}, false
	}
	return ModelOfferIdentity{
		ModelName: candidate.RealModel,
		Kind:      kind,
		SourceID:  candidate.SourceID,
	}, true
}

func plannedSupportedEndpoints(
	existing []SupportedEndpoint,
	modes []relayplan.ModelOfferCandidateMode,
) []SupportedEndpoint {
	if len(existing) > 0 {
		return append([]SupportedEndpoint(nil), existing...)
	}
	available := make(map[SupportedEndpoint]struct{}, len(existing)+len(modes))
	for _, endpoint := range existing {
		available[endpoint] = struct{}{}
	}
	for _, mode := range modes {
		if mode.Mode == "" {
			continue
		}
		switch mode.Protocol {
		case llmkit.ProtocolOpenAIChat:
			available[EndpointChatCompletions] = struct{}{}
		case llmkit.ProtocolOpenAIResponses:
			available[EndpointResponses] = struct{}{}
		case llmkit.ProtocolClaude:
			available[EndpointMessages] = struct{}{}
		}
	}
	result := make([]SupportedEndpoint, 0, len(available))
	for _, entry := range supportedEndpointRegistry {
		if _, ok := available[entry.endpoint]; ok {
			result = append(result, entry.endpoint)
		}
	}
	return result
}

func applyOfferPlansToRouting(
	routingModels []RoutingModel,
	plans marketplaceOfferPlans,
	catalog []MarketplaceModel,
	retainDiagnostics bool,
) ([]RoutingModel, map[string]bool) {
	offers := indexMarketplaceOffers(catalog)
	runtimeRoutings := make(map[string]bool, len(routingModels))
	result := make([]RoutingModel, 0, len(routingModels))
	for _, model := range routingModels {
		plan, known := plans.byRequestModel[model.ModelName]
		runtimeRouting := known && plan.Routing
		runtimeRoutings[model.ModelName] = runtimeRouting
		model.ReachableRealModels = []string{}
		model.FlattenedDestinations = []FlattenedDestination{}
		if runtimeRouting {
			model.FlattenedDestinations = routingDestinationsFromPlan(plan, offers)
			for _, destination := range model.FlattenedDestinations {
				model.ReachableRealModels = append(model.ReachableRealModels, destination.ModelName)
			}
		}
		if len(model.FlattenedDestinations) == 0 {
			model.RoutingWarnings = appendRoutingWarning(model.RoutingWarnings, RoutingWarningNoVisibleOffer)
		}
		if retainDiagnostics || (runtimeRouting && len(model.FlattenedDestinations) > 0) {
			result = append(result, model)
		}
	}
	return result, runtimeRoutings
}

func indexMarketplaceOffers(catalog []MarketplaceModel) map[ModelOfferIdentity]ModelOffer {
	result := make(map[ModelOfferIdentity]ModelOffer)
	for _, model := range catalog {
		for _, offer := range model.Offers {
			result[offer.Identity] = offer
		}
	}
	return result
}

func routingDestinationsFromPlan(
	plan relayplan.ModelOfferPlan,
	offers map[ModelOfferIdentity]ModelOffer,
) []FlattenedDestination {
	byModel := make(map[string]map[string]ModelOfferSummary)
	for _, candidate := range plan.Candidates {
		identity, ok := modelOfferIdentityFromCandidate(candidate)
		if !ok {
			continue
		}
		offer, ok := offers[identity]
		if !ok {
			continue
		}
		offer.Available = true
		offer.SupportedEndpoints = plannedSupportedEndpoints(offer.SupportedEndpoints, candidate.Modes)
		if len(offer.SupportedEndpoints) == 0 {
			continue
		}
		if byModel[identity.ModelName] == nil {
			byModel[identity.ModelName] = make(map[string]ModelOfferSummary)
		}
		byModel[identity.ModelName][offer.OfferRef] = routingOfferSummary(offer)
	}
	models := make([]string, 0, len(byModel))
	for modelName := range byModel {
		models = append(models, modelName)
	}
	sort.Strings(models)
	result := make([]FlattenedDestination, 0, len(models))
	for _, modelName := range models {
		summaries := make([]ModelOfferSummary, 0, len(byModel[modelName]))
		for _, summary := range byModel[modelName] {
			summaries = append(summaries, summary)
		}
		sort.Slice(summaries, func(i, j int) bool { return summaries[i].OfferRef < summaries[j].OfferRef })
		result = append(result, FlattenedDestination{ModelName: modelName, Offers: summaries})
	}
	return result
}

func appendRoutingWarning(warnings []RoutingWarning, warning RoutingWarning) []RoutingWarning {
	set := make(map[RoutingWarning]struct{}, len(warnings)+1)
	for _, existing := range warnings {
		set[existing] = struct{}{}
	}
	set[warning] = struct{}{}
	return sortedRoutingWarnings(set)
}

func sortedUniqueMarketplaceNames(names []string) []string {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for name := range set {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
