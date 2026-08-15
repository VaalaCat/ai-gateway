package model_marketplace

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	pricecontract "github.com/VaalaCat/ai-gateway/internal/pkg/pricing"
	"github.com/gin-gonic/gin"
)

type handlerViewerGate interface {
	RequireUser(*app.Context, uint) (MarketplaceViewer, error)
	RequireAdmin(*app.Context, *uint) (MarketplaceViewer, error)
}

type handlerCatalogFinder interface {
	Find(context.Context, MarketplaceViewer) ([]MarketplaceModel, error)
}

type handlerRoutingFinder interface {
	Find(context.Context, MarketplaceViewer, map[string][]ModelOffer) ([]RoutingModel, error)
}

type handlerPriceFinder interface {
	Find(MarketplaceViewer, *models.ModelConfig, ModelOffer, OfferBilling) (OfferPricing, error)
}

type handlerPerformanceFinder interface {
	Find(context.Context, []ModelOffer, UsageWindow, time.Time) (ModelPerformanceResult, error)
}

type handlerUsageFinder interface {
	Find(context.Context, MarketplaceViewer, []ModelOffer, UsageWindow) (map[string][]OfferUsageReference, error)
}

// Handler composes Tasks 2-6. It is process-scoped: Server constructs it once,
// and its performance finder retains the same process cache for every request.
type Handler struct {
	gate             handlerViewerGate
	catalog          handlerCatalogFinder
	routing          handlerRoutingFinder
	pricing          handlerPriceFinder
	performance      handlerPerformanceFinder
	usage            handlerUsageFinder
	encoder          OfferRefEncoder
	settings         PerformanceSettings
	clock            func() time.Time
	performanceCache *GlobalModelPerformanceCache
	offerPlanFinder  atomic.Pointer[modelOfferPlanFinderSnapshot]
}

func NewHandler(
	query dao.ModelMarketplaceQuery,
	usageQuery dao.ModelMarketplaceUsageQuery,
	performanceCache *GlobalModelPerformanceCache,
	settings PerformanceSettings,
	jwtSecret string,
) (*Handler, error) {
	if query == nil {
		return nil, errors.New("model marketplace query is required")
	}
	if usageQuery == nil {
		return nil, errors.New("model marketplace usage query is required")
	}
	if performanceCache == nil {
		return nil, errors.New("model marketplace performance cache is required")
	}
	encoder, err := NewHMACOfferRefEncoder(jwtSecret)
	if err != nil {
		return nil, err
	}
	catalog := NewModelCatalogFinder(query, encoder)
	routing := NewRoutingModelFinder(query)
	return &Handler{
		gate:             NewMarketplaceAccessGate(),
		catalog:          catalog,
		routing:          routing,
		pricing:          ModelPriceFinder{},
		performance:      NewModelPerformanceFinder(performanceCache, settings),
		usage:            NewOfferUsageReferenceFinder(usageQuery),
		encoder:          encoder,
		settings:         settings,
		clock:            time.Now,
		performanceCache: performanceCache,
	}, nil
}

func (h *Handler) PerformanceCache() *GlobalModelPerformanceCache {
	if h == nil {
		return nil
	}
	return h.performanceCache
}

// UserFeatureEnabledMiddleware keeps the disabled feature boundary ahead of
// query binding. RequireUser repeats the same in-process check so direct
// Handler calls and future adapters remain fail-closed.
func (h *Handler) UserFeatureEnabledMiddleware(adapter *api.Adapter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h != nil && modelMarketplaceEnabled(h.settings) {
			c.Next()
			return
		}
		status, body := adapter.ErrorMapper.Map(api.NotFoundError(consts.ErrNotFound))
		adapter.Writer.WriteJSON(c, status, body)
		c.Abort()
	}
}

func modelMarketplaceEnabled(settings PerformanceSettings) bool {
	if settings == nil {
		return false
	}
	raw, ok := settings.Lookup(consts.SettingKeyModelMarketplaceEnabled)
	if !ok {
		return consts.ModelMarketplaceDefaultEnabled
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return consts.ModelMarketplaceDefaultEnabled
	}
}

type composedMarketplace struct {
	viewer  MarketplaceViewer
	real    []composedRealModel
	routing []RoutingModel
}

type composedRealModel struct {
	model       MarketplaceModel
	offers      []composedOffer
	performance ModelPerformanceResult
}

type composedOffer struct {
	offer   ModelOffer
	pricing OfferPricing
	usage   []OfferUsageReference
}

type pricedRealModels struct {
	catalog        []MarketplaceModel
	composedByName map[string]composedRealModel
	offerRefs      map[string]struct{}
}

func (h *Handler) compose(
	ctx context.Context,
	viewer MarketplaceViewer,
	window UsageWindow,
	retainDiagnostics bool,
) (composedMarketplace, error) {
	directory, err := h.buildMarketplaceDirectory(ctx, viewer, retainDiagnostics)
	if err != nil {
		return composedMarketplace{}, err
	}
	return h.enrichMarketplacePerformance(ctx, directory, window)
}

func (h *Handler) buildMarketplaceDirectory(
	ctx context.Context,
	viewer MarketplaceViewer,
	retainDiagnostics bool,
) (composedMarketplace, error) {
	if h == nil || h.catalog == nil || h.routing == nil || h.pricing == nil || h.performance == nil {
		return composedMarketplace{}, errors.New("model marketplace handler dependencies are required")
	}
	planFinder := h.modelOfferPlanFinder()
	if planFinder == nil && !retainDiagnostics {
		return emptyComposedMarketplace(viewer), nil
	}
	rawCatalog, err := h.catalog.Find(ctx, viewer)
	if err != nil {
		return composedMarketplace{}, fmt.Errorf("find marketplace catalog: %w", err)
	}
	offersByModel := make(map[string][]ModelOffer, len(rawCatalog))
	for _, model := range rawCatalog {
		offersByModel[model.ModelName] = append([]ModelOffer(nil), model.Offers...)
	}
	routingModels, err := h.routing.Find(ctx, viewer, offersByModel)
	if err != nil {
		return composedMarketplace{}, fmt.Errorf("find marketplace routing models: %w", err)
	}
	requestModels := make([]string, 0, len(rawCatalog)+len(routingModels))
	for _, model := range rawCatalog {
		requestModels = append(requestModels, model.ModelName)
	}
	for _, model := range routingModels {
		requestModels = append(requestModels, model.ModelName)
	}
	plans := findMarketplaceOfferPlans(ctx, planFinder, viewer, requestModels)
	catalog := applyOfferPlansToCatalog(rawCatalog, plans, retainDiagnostics)
	routingModels, runtimeRoutings := applyOfferPlansToRouting(routingModels, plans, rawCatalog, retainDiagnostics)
	priced, err := h.buildPricedRealModels(viewer, catalog, retainDiagnostics)
	if err != nil {
		return composedMarketplace{}, err
	}
	catalog, routingModels = pickRuntimeMarketplaceResources(priced.catalog, routingModels, runtimeRoutings)

	result := composedMarketplace{
		viewer: viewer, real: make([]composedRealModel, 0, len(catalog)),
		routing: append([]RoutingModel(nil), routingModels...),
	}
	for _, model := range catalog {
		composed, exists := priced.composedByName[model.ModelName]
		if !exists {
			return composedMarketplace{}, errors.New("priced marketplace model is required")
		}
		result.real = append(result.real, composed)
	}
	result.routing = filterRoutingModelsToPricedOffers(result.routing, priced.offerRefs, retainDiagnostics)
	sort.Slice(result.real, func(i, j int) bool {
		return result.real[i].model.ModelName < result.real[j].model.ModelName
	})
	sort.Slice(result.routing, func(i, j int) bool {
		return result.routing[i].ModelName < result.routing[j].ModelName
	})
	return result, nil
}

func (h *Handler) enrichMarketplacePerformance(
	ctx context.Context,
	directory composedMarketplace,
	window UsageWindow,
) (composedMarketplace, error) {
	if h == nil || h.performance == nil {
		return composedMarketplace{}, errors.New("model marketplace performance finder is required")
	}
	now := time.Now()
	if h.clock != nil {
		now = h.clock()
	}
	allOffers := make([]ModelOffer, 0)
	for _, model := range directory.real {
		for _, offer := range composedOffers(model) {
			if offer.Available {
				allOffers = append(allOffers, offer)
			}
		}
	}
	if len(allOffers) == 0 {
		empty := emptyMarketplacePerformance(window, now)
		for index := range directory.real {
			projected, err := performanceForComposedModel(empty, directory.real[index])
			if err != nil {
				return composedMarketplace{}, fmt.Errorf(
					"project empty model %q performance: %w",
					directory.real[index].model.ModelName,
					err,
				)
			}
			directory.real[index].performance = projected
		}
		return directory, nil
	}
	performance, performanceErr := h.performance.Find(ctx, allOffers, window, now)
	performance = degradablePerformanceResult(performance, performanceErr, len(allOffers), window, now, true)
	for index := range directory.real {
		projected, projectionErr := performanceForComposedModel(performance, directory.real[index])
		if projectionErr != nil {
			degraded := degradablePerformanceResult(
				projected, projectionErr, len(directory.real[index].offers), window, now, false,
			)
			projected, projectionErr = performanceForComposedModel(degraded, directory.real[index])
			if projectionErr != nil {
				return composedMarketplace{}, fmt.Errorf("project degraded model %q performance: %w", directory.real[index].model.ModelName, projectionErr)
			}
		}
		directory.real[index].performance = projected
	}
	return directory, nil
}

func emptyMarketplacePerformance(window UsageWindow, observedUntil time.Time) ModelPerformanceResult {
	return ModelPerformanceResult{
		PerformanceStatus: PerformanceAvailable,
		ObservedUntil:     observedUntil.UTC(),
		ModelStatus:       MarketplaceHealthUnknown,
		Offers:            map[string]OfferPerformanceSnapshot{},
		offerComponents:   map[string][]HourlyPerformanceComponents{},
		projectionWindow:  window,
		minSamples:        consts.ModelMarketplaceDefaultMinSamples,
	}
}

func (h *Handler) buildPricedRealModels(
	viewer MarketplaceViewer,
	catalog []MarketplaceModel,
	retainDiagnostics bool,
) (pricedRealModels, error) {
	result := pricedRealModels{
		catalog:        make([]MarketplaceModel, 0, len(catalog)),
		composedByName: make(map[string]composedRealModel, len(catalog)),
		offerRefs:      make(map[string]struct{}),
	}
	for _, model := range catalog {
		composed, err := h.composeRealModel(viewer, model)
		if err != nil {
			return pricedRealModels{}, err
		}
		if len(composed.offers) == 0 && !retainDiagnostics {
			continue
		}
		result.catalog = append(result.catalog, composed.model)
		result.composedByName[model.ModelName] = composed
		for _, offer := range composed.offers {
			result.offerRefs[offer.offer.OfferRef] = struct{}{}
		}
	}
	return result, nil
}

func emptyComposedMarketplace(viewer MarketplaceViewer) composedMarketplace {
	return composedMarketplace{
		viewer:  viewer,
		real:    []composedRealModel{},
		routing: []RoutingModel{},
	}
}

func pickRuntimeMarketplaceResources(
	realModels []MarketplaceModel,
	routingModels []RoutingModel,
	runtimeRoutings map[string]bool,
) ([]MarketplaceModel, []RoutingModel) {
	realNames := make(map[string]struct{}, len(realModels))
	for _, model := range realModels {
		realNames[model.ModelName] = struct{}{}
	}

	hiddenRealNames := make(map[string]struct{})
	pickedRouting := make([]RoutingModel, 0, len(routingModels))
	for _, model := range routingModels {
		if _, sameNameReal := realNames[model.ModelName]; !sameNameReal {
			pickedRouting = append(pickedRouting, model)
			continue
		}
		if !runtimeRoutings[model.ModelName] {
			continue
		}
		hiddenRealNames[model.ModelName] = struct{}{}
		pickedRouting = append(pickedRouting, model)
	}

	pickedReal := make([]MarketplaceModel, 0, len(realModels)-len(hiddenRealNames))
	for _, model := range realModels {
		if _, hidden := hiddenRealNames[model.ModelName]; !hidden {
			pickedReal = append(pickedReal, model)
		}
	}
	return pickedReal, pickedRouting
}

func (h *Handler) composeRealModel(
	viewer MarketplaceViewer,
	model MarketplaceModel,
) (composedRealModel, error) {
	offers := append([]ModelOffer(nil), model.Offers...)
	sort.Slice(offers, func(i, j int) bool {
		if offers[i].Kind != offers[j].Kind {
			return offers[i].Kind < offers[j].Kind
		}
		if offers[i].DisplayName != offers[j].DisplayName {
			return offers[i].DisplayName < offers[j].DisplayName
		}
		return offers[i].OfferRef < offers[j].OfferRef
	})
	composed := composedRealModel{model: model, offers: make([]composedOffer, 0, len(offers))}
	for _, offer := range offers {
		billing, err := h.billingForOffer(offer)
		if err != nil {
			return composedRealModel{}, fmt.Errorf("build marketplace offer pricing facts: %w", err)
		}
		pricing, err := h.pricing.Find(viewer, &model.Config, offer, billing)
		if err != nil {
			if errors.Is(err, pricecontract.ErrInvalidModelPrices) {
				continue
			}
			return composedRealModel{}, fmt.Errorf("price marketplace offer: %w", err)
		}
		composed.offers = append(composed.offers, composedOffer{offer: offer, pricing: pricing, usage: []OfferUsageReference{}})
	}
	composed.model = marketplaceModelWithPricedOffers(model, composed.offers)
	return composed, nil
}

func marketplaceModelWithPricedOffers(model MarketplaceModel, offers []composedOffer) MarketplaceModel {
	model.Offers = make([]ModelOffer, 0, len(offers))
	model.AvailableOfferCount = 0
	model.PlatformOfferCount = 0
	model.PrivateOfferCount = 0
	for _, composed := range offers {
		model.Offers = append(model.Offers, composed.offer)
		if !composed.offer.Available {
			continue
		}
		model.AvailableOfferCount++
		if composed.offer.Kind == OfferKindPlatform {
			model.PlatformOfferCount++
		} else if composed.offer.Kind == OfferKindPrivate {
			model.PrivateOfferCount++
		}
	}
	return model
}

func filterRoutingModelsToPricedOffers(
	models []RoutingModel,
	pricedOfferRefs map[string]struct{},
	retainDiagnostics bool,
) []RoutingModel {
	result := make([]RoutingModel, 0, len(models))
	for _, model := range models {
		destinations := make([]FlattenedDestination, 0, len(model.FlattenedDestinations))
		reachableModels := make([]string, 0, len(model.ReachableRealModels))
		for _, destination := range model.FlattenedDestinations {
			offers := make([]ModelOfferSummary, 0, len(destination.Offers))
			for _, offer := range destination.Offers {
				if _, priced := pricedOfferRefs[offer.OfferRef]; priced {
					offers = append(offers, offer)
				}
			}
			if len(offers) == 0 {
				continue
			}
			destinations = append(destinations, FlattenedDestination{
				ModelName: destination.ModelName,
				Offers:    offers,
			})
			reachableModels = append(reachableModels, destination.ModelName)
		}
		model.FlattenedDestinations = destinations
		model.ReachableRealModels = reachableModels
		if len(destinations) == 0 {
			model.RoutingWarnings = appendRoutingWarning(model.RoutingWarnings, RoutingWarningNoVisibleOffer)
		}
		if retainDiagnostics || len(destinations) > 0 {
			result = append(result, model)
		}
	}
	return result
}

func performanceForComposedModel(batch ModelPerformanceResult, model composedRealModel) (ModelPerformanceResult, error) {
	result := ModelPerformanceResult{
		PerformanceStatus: batch.PerformanceStatus,
		RefreshedAt:       batch.RefreshedAt,
		ObservedUntil:     batch.ObservedUntil,
		ModelStatus:       MarketplaceHealthUnknown,
		Offers:            make(map[string]OfferPerformanceSnapshot, len(model.offers)),
		Model: ModelPerformanceSnapshot{
			PerformanceStatus: batch.PerformanceStatus,
			Window:            batch.projectionWindow, StatusBuckets: []ModelPerformanceStatusBucket{},
		},
		offerComponents:  make(map[string][]HourlyPerformanceComponents, len(model.offers)),
		projectionWindow: batch.projectionWindow,
		minSamples:       batch.minSamples,
	}
	statuses := make([]MarketplaceHealthStatus, 0, len(model.offers))
	componentsByOffer := make([][]HourlyPerformanceComponents, 0, len(model.offers))
	for _, offer := range model.offers {
		if !offer.offer.Available {
			continue
		}
		snapshot, exists := batch.Offers[offer.offer.OfferRef]
		if !exists {
			continue
		}
		result.Offers[offer.offer.OfferRef] = snapshot
		statuses = append(statuses, snapshot.Summary.Status)
		if components, exists := batch.offerComponents[offer.offer.OfferRef]; exists {
			copied := append([]HourlyPerformanceComponents(nil), components...)
			result.offerComponents[offer.offer.OfferRef] = copied
			componentsByOffer = append(componentsByOffer, copied)
		}
	}
	result.ModelStatus = aggregateVisiblePerformanceStatus(statuses)
	if result.ObservedUntil.IsZero() || result.projectionWindow == "" {
		return result, nil
	}
	projection, err := projectModelPerformance(
		componentsByOffer, result.ObservedUntil, result.projectionWindow, result.minSamples,
	)
	if err != nil {
		return result, fmt.Errorf("project composed model performance: %w", err)
	}
	projection.PerformanceStatus = result.PerformanceStatus
	result.Model = projection
	return result, nil
}

func (h *Handler) billingForOffer(offer ModelOffer) (OfferBilling, error) {
	if offer.Facts.Billing.Identity != offer.Identity {
		return OfferBilling{}, errors.New("marketplace offer billing identity does not match offer identity")
	}
	if offer.Kind == OfferKindPlatform {
		return offer.Facts.Billing, nil
	}
	if offer.Kind != OfferKindPrivate {
		return OfferBilling{}, fmt.Errorf("unsupported marketplace offer kind %q", offer.Kind)
	}
	mode := consts.BYOKDefaultBillingMode
	ratio := consts.BYOKDefaultServiceFeeRatioFloat
	if h.settings != nil {
		if raw, ok := h.settings.Lookup(consts.SettingKeyBYOKBillingMode); ok {
			candidate := strings.TrimSpace(raw)
			if candidate == consts.BYOKBillingModeFree || candidate == consts.BYOKBillingModeServiceFee {
				mode = candidate
			}
		}
		if raw, ok := h.settings.Lookup(consts.SettingKeyBYOKServiceFeeRatio); ok {
			if candidate, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil && candidate >= 0 && candidate <= 1 {
				ratio = candidate
			}
		}
	}
	return OfferBilling{
		Identity: offer.Identity, BYOKMode: mode, BYOKServiceFeeRatio: ratio,
	}, nil
}

func degradablePerformanceResult(
	result ModelPerformanceResult,
	err error,
	offerCount int,
	window UsageWindow,
	fallbackObservedUntil time.Time,
	preserveCompleteStale bool,
) ModelPerformanceResult {
	completeStale := result.PerformanceStatus == PerformanceStale && len(result.Offers) == offerCount
	if err == nil || (preserveCompleteStale && completeStale) {
		if result.Offers == nil {
			result.Offers = map[string]OfferPerformanceSnapshot{}
		}
		return result
	}
	if window == "" {
		window = result.projectionWindow
	}
	if fallbackObservedUntil.IsZero() {
		fallbackObservedUntil = time.Now()
	}
	if result.ObservedUntil.IsZero() {
		result.ObservedUntil = fallbackObservedUntil.UTC()
	}
	minSamples := result.minSamples
	if minSamples < 1 {
		minSamples = consts.ModelMarketplaceDefaultMinSamples
	}
	return ModelPerformanceResult{
		PerformanceStatus: PerformanceUnavailable,
		ObservedUntil:     result.ObservedUntil,
		ModelStatus:       MarketplaceHealthUnknown,
		Offers:            map[string]OfferPerformanceSnapshot{},
		offerComponents:   map[string][]HourlyPerformanceComponents{},
		projectionWindow:  window,
		minSamples:        minSamples,
	}
}

func (m *composedRealModel) attachUsage(references map[string][]OfferUsageReference) {
	for index := range m.offers {
		m.offers[index].usage = append([]OfferUsageReference(nil), references[m.offers[index].offer.OfferRef]...)
		if m.offers[index].usage == nil {
			m.offers[index].usage = []OfferUsageReference{}
		}
	}
}

func (m *composedRealModel) selectOffer(ref string, encoder OfferRefEncoder) bool {
	if strings.TrimSpace(ref) == "" {
		return true
	}
	selected := make([]composedOffer, 0, 1)
	for _, offer := range m.offers {
		if encoder != nil && encoder.Matches(ref, offer.offer.Identity) {
			selected = append(selected, offer)
		}
	}
	if len(selected) != 1 {
		return false
	}
	m.offers = selected
	performance, projectionErr := performanceForComposedModel(m.performance, *m)
	if projectionErr != nil {
		degraded := degradablePerformanceResult(
			performance, projectionErr, len(m.offers), m.performance.projectionWindow, time.Now(), false,
		)
		performance, projectionErr = performanceForComposedModel(degraded, *m)
	}
	if projectionErr != nil {
		return false
	}
	m.performance = performance
	m.model.Offers = []ModelOffer{selected[0].offer}
	m.model.AvailableOfferCount = 0
	m.model.PlatformOfferCount = 0
	m.model.PrivateOfferCount = 0
	if selected[0].offer.Available {
		m.model.AvailableOfferCount = 1
		if selected[0].offer.Kind == OfferKindPlatform {
			m.model.PlatformOfferCount = 1
		} else {
			m.model.PrivateOfferCount = 1
		}
	}
	return true
}

func composedOffers(model composedRealModel) []ModelOffer {
	offers := make([]ModelOffer, 0, len(model.offers))
	for _, offer := range model.offers {
		offers = append(offers, offer.offer)
	}
	return offers
}
