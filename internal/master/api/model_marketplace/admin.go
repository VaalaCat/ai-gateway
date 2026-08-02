package model_marketplace

import (
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type AdminMarketplaceMode string

const (
	AdminModeGlobal       AdminMarketplaceMode = "global"
	AdminModeTokenPreview AdminMarketplaceMode = "token_preview"
)

type AdminListRequest struct {
	TokenID  *uint                `form:"token_id"`
	Search   string               `form:"search"`
	Provider string               `form:"provider"`
	Kind     MarketplaceModelKind `form:"kind"`
}

type AdminDetailRequest struct {
	TokenID  *uint  `form:"token_id"`
	Model    string `form:"model"`
	Window   string `form:"window"`
	OfferRef string `form:"offer_ref"`
}

type AdminSelectedTokenDTO struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type AdminMarketplaceViewDTO struct {
	Mode          AdminMarketplaceMode   `json:"mode"`
	SelectedToken *AdminSelectedTokenDTO `json:"selected_token"`
}

type AdminMarketplaceFiltersDTO struct {
	Providers        []string `json:"providers"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

type AdminMarketplaceListResponse struct {
	View    AdminMarketplaceViewDTO    `json:"view"`
	Models  []AdminMarketplaceModelDTO `json:"models"`
	Filters AdminMarketplaceFiltersDTO `json:"filters"`
}

type AdminModelDetailResponse struct {
	View        AdminMarketplaceViewDTO  `json:"view"`
	Window      UsageWindow              `json:"window"`
	UsageStatus UsageAvailability        `json:"usage_status"`
	Model       AdminMarketplaceModelDTO `json:"model"`
}

// Administrator DTOs intentionally do not embed or alias ordinary response
// DTOs. Their diagnostics are explicit, and credentials have no destination.
type AdminMarketplaceModelDTO struct {
	Kind    MarketplaceModelKind  `json:"kind"`
	Real    *AdminRealModelDTO    `json:"real,omitempty"`
	Routing *AdminRoutingModelDTO `json:"routing,omitempty"`
}

type AdminRealModelDTO struct {
	ModelName           string                   `json:"model_name"`
	Metadata            AdminModelMetadataDTO    `json:"metadata"`
	AggregateStatus     MarketplaceHealthStatus  `json:"aggregate_status"`
	Performance         AdminModelPerformanceDTO `json:"performance"`
	AvailableOfferCount int                      `json:"available_offer_count"`
	PlatformOfferCount  int                      `json:"platform_offer_count"`
	PrivateOfferCount   int                      `json:"private_offer_count"`
	Offers              []AdminModelOfferDTO     `json:"offers"`
}

type AdminModelPerformanceDTO struct {
	PerformanceStatus PerformanceStatus                `json:"performance_status"`
	Window            UsageWindow                      `json:"window"`
	SuccessRate       *float64                         `json:"success_rate"`
	CacheHitRate      *float64                         `json:"cache_hit_rate"`
	StatusHistory     []AdminModelPerformanceStatusDTO `json:"status_history"`
}

type AdminModelPerformanceStatusDTO struct {
	StartedAt   int64                   `json:"started_at"`
	EndedAt     int64                   `json:"ended_at"`
	SuccessRate *float64                `json:"success_rate"`
	Status      MarketplaceHealthStatus `json:"status"`
	InProgress  bool                    `json:"in_progress"`
}

type AdminModelMetadataDTO struct {
	DisplayName         string   `json:"display_name"`
	Description         string   `json:"description"`
	Provider            string   `json:"provider"`
	InputModalities     []string `json:"input_modalities"`
	OutputModalities    []string `json:"output_modalities"`
	ContextLength       int64    `json:"context_length"`
	MaxOutputTokens     int64    `json:"max_output_tokens"`
	SupportedParameters []string `json:"supported_parameters"`
	ToolCalling         bool     `json:"tool_calling"`
	StructuredOutput    bool     `json:"structured_output"`
	Reasoning           bool     `json:"reasoning"`
	PromptCache         bool     `json:"prompt_cache"`
}

type AdminModelOfferDTO struct {
	OfferRef           string                        `json:"offer_ref"`
	Kind               ModelOfferKind                `json:"kind"`
	DisplayName        string                        `json:"display_name"`
	Ownership          ModelOfferOwnership           `json:"ownership"`
	Available          bool                          `json:"available"`
	SupportedEndpoints []SupportedEndpoint           `json:"supported_endpoints"`
	Pricing            AdminOfferPricingDTO          `json:"pricing"`
	PerformanceStatus  PerformanceStatus             `json:"performance_status"`
	Performance        AdminPerformanceSummaryDTO    `json:"performance"`
	StatusHistory      []AdminPerformanceStatusDTO   `json:"status_history"`
	TrendSeries        []AdminPerformanceTrendDTO    `json:"trend_series"`
	UsageReferences    []AdminOfferUsageReferenceDTO `json:"usage_references"`
	Diagnostics        AdminOfferDiagnosticsDTO      `json:"diagnostics"`
}

type AdminOfferDiagnosticsDTO struct {
	ChannelID         uint                   `json:"channel_id"`
	PrivateChannelID  uint                   `json:"private_channel_id"`
	InternalName      string                 `json:"internal_name"`
	PublicDisplayName string                 `json:"public_display_name"`
	OwnerID           uint                   `json:"owner_id"`
	BaseURL           string                 `json:"base_url"`
	EndpointPaths     []AdminEndpointPathDTO `json:"endpoint_paths"`
	DisabledReasons   []string               `json:"disabled_reasons"`
}

type AdminEndpointPathDTO struct {
	Endpoint SupportedEndpoint `json:"endpoint"`
	Path     string            `json:"path"`
}

type AdminModelPricesDTO struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

type AdminOfferPricingDTO struct {
	ReferencePrice AdminModelPricesDTO `json:"reference_price"`
	GatewayCharge  AdminModelPricesDTO `json:"gateway_charge"`
	EstimatedTotal AdminModelPricesDTO `json:"estimated_total"`
	Accuracy       OfferAccuracy       `json:"accuracy"`
}

type AdminTokenUnitsDTO struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Total      int64 `json:"total"`
}

type AdminPerformanceSummaryDTO struct {
	Status              MarketplaceHealthStatus `json:"status"`
	SuccessRate         *float64                `json:"success_rate"`
	TTFTAvgMs           *float64                `json:"ttft_avg_ms"`
	TTFTP95Ms           *int64                  `json:"ttft_p95_ms"`
	TPSAvg              *float64                `json:"tps_avg"`
	TPSP5               *float64                `json:"tps_p5"`
	DurationP95Ms       *int64                  `json:"duration_p95_ms"`
	TokenUnits          AdminTokenUnitsDTO      `json:"token_units"`
	RequestCount        int64                   `json:"request_count"`
	SuccessCount        int64                   `json:"success_count"`
	FailureCount        int64                   `json:"failure_count"`
	StreamRequestCount  int64                   `json:"stream_request_count"`
	TTFTSampleCount     int64                   `json:"ttft_sample_count"`
	TPSSampleCount      int64                   `json:"tps_sample_count"`
	DurationSampleCount int64                   `json:"duration_sample_count"`
}

type AdminPerformanceStatusDTO struct {
	StartedAt  int64                   `json:"started_at"`
	EndedAt    int64                   `json:"ended_at"`
	Status     MarketplaceHealthStatus `json:"status"`
	InProgress bool                    `json:"in_progress"`
}

type AdminPerformanceTrendDTO struct {
	StartedAt   int64                   `json:"started_at"`
	EndedAt     int64                   `json:"ended_at"`
	Status      MarketplaceHealthStatus `json:"status"`
	InProgress  bool                    `json:"in_progress"`
	SuccessRate *float64                `json:"success_rate"`
	TTFTAvgMs   *float64                `json:"ttft_avg_ms"`
	TPSAvg      *float64                `json:"tps_avg"`
	TokenUnits  AdminTokenUnitsDTO      `json:"token_units"`
}

type AdminOfferUsageReferenceDTO struct {
	Scope               UsageScope         `json:"scope"`
	Window              UsageWindow        `json:"window"`
	TokenUnits          AdminTokenUnitsDTO `json:"token_units"`
	ReferenceCost       *int64             `json:"reference_cost"`
	GatewayChargeCost   int64              `json:"gateway_charge_cost"`
	EstimatedTotalCost  *int64             `json:"estimated_total_cost"`
	Accuracy            OfferAccuracy      `json:"accuracy"`
	IncludesSharedUsage bool               `json:"includes_shared_usage"`
}

type AdminRoutingModelDTO struct {
	ModelName             string                       `json:"model_name"`
	DisplayName           string                       `json:"display_name"`
	ReachableRealModels   []string                     `json:"reachable_real_models"`
	FlattenedDestinations []AdminRoutingDestinationDTO `json:"flattened_destinations"`
	RoutingWarnings       []RoutingWarning             `json:"routing_warnings"`
	Guidance              RoutingModelGuidance         `json:"guidance"`
	Diagnostics           AdminRoutingDiagnosticsDTO   `json:"diagnostics"`
}

type AdminRoutingDiagnosticsDTO struct {
	Definitions []AdminRoutingDefinitionDTO `json:"definitions"`
}

type AdminRoutingDefinitionDTO struct {
	OccurrenceID string                  `json:"occurrence_id"`
	Path         []AdminRoutingPathDTO   `json:"path"`
	RoutingID    uint                    `json:"routing_id"`
	Name         string                  `json:"name"`
	Scope        string                  `json:"scope"`
	UserID       uint                    `json:"user_id"`
	TokenID      uint                    `json:"token_id"`
	Enabled      bool                    `json:"enabled"`
	Members      []AdminRoutingMemberDTO `json:"members"`
}

type AdminRoutingPathDTO struct {
	Ref       string `json:"ref"`
	RoutingID uint   `json:"routing_id"`
}

type AdminRoutingMemberDTO struct {
	Ref       string                  `json:"ref"`
	Priority  int                     `json:"priority"`
	Weight    int                     `json:"weight"`
	Kind      RoutingMemberTargetKind `json:"kind"`
	ModelName string                  `json:"model_name,omitempty"`
	RoutingID uint                    `json:"routing_id,omitempty"`
}

type AdminRoutingDestinationDTO struct {
	ModelName string                 `json:"model_name"`
	Offers    []AdminRoutingOfferDTO `json:"offers"`
}

type AdminRoutingOfferDTO struct {
	OfferRef           string              `json:"offer_ref"`
	Kind               ModelOfferKind      `json:"kind"`
	DisplayName        string              `json:"display_name"`
	Ownership          ModelOfferOwnership `json:"ownership"`
	Available          bool                `json:"available"`
	SupportedEndpoints []SupportedEndpoint `json:"supported_endpoints"`
}

func (h *Handler) AdminList(c *app.Context, req AdminListRequest) (AdminMarketplaceListResponse, error) {
	viewer, err := h.gate.RequireAdmin(c, req.TokenID)
	if err != nil {
		return AdminMarketplaceListResponse{}, err
	}
	if err := validateListKind(req.Kind); err != nil {
		return AdminMarketplaceListResponse{}, err
	}
	composed, err := h.compose(c.RequestContext(), viewer, UsageWindow24Hours, true)
	if err != nil {
		return AdminMarketplaceListResponse{}, err
	}
	return mapAdminList(composed, req), nil
}

func (h *Handler) AdminDetail(c *app.Context, req AdminDetailRequest) (AdminModelDetailResponse, error) {
	viewer, err := h.gate.RequireAdmin(c, req.TokenID)
	if err != nil {
		return AdminModelDetailResponse{}, err
	}
	window, err := normalizedUsageWindow(req.Window)
	if err != nil {
		return AdminModelDetailResponse{}, err
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return AdminModelDetailResponse{}, api.BadRequestError("marketplace model is required", nil)
	}
	composed, err := h.compose(c.RequestContext(), viewer, window, true)
	if err != nil {
		return AdminModelDetailResponse{}, err
	}
	for index := range composed.real {
		if composed.real[index].model.ModelName != modelName {
			continue
		}
		if !composed.real[index].selectOffer(req.OfferRef, h.encoder) {
			return AdminModelDetailResponse{}, api.NotFoundError(consts.ErrNotFound)
		}
		usageStatus := UsageAvailable
		if h.usage == nil {
			usageStatus = UsageUnavailable
		} else {
			references, usageErr := h.usage.Find(c.RequestContext(), viewer, composedOffers(composed.real[index]), window)
			if usageErr != nil {
				usageStatus = UsageUnavailable
			} else {
				composed.real[index].attachUsage(references)
			}
		}
		return AdminModelDetailResponse{
			View: adminMarketplaceView(viewer), Window: window, UsageStatus: usageStatus,
			Model: mapAdminRealModel(composed.real[index]),
		}, nil
	}
	if strings.TrimSpace(req.OfferRef) != "" {
		return AdminModelDetailResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	for _, routingModel := range composed.routing {
		if routingModel.ModelName == modelName {
			return AdminModelDetailResponse{
				View: adminMarketplaceView(viewer), Window: window, UsageStatus: UsageNotApplicable,
				Model: mapAdminRoutingModel(routingModel),
			}, nil
		}
	}
	return AdminModelDetailResponse{}, api.NotFoundError(consts.ErrNotFound)
}

func mapAdminList(composed composedMarketplace, req AdminListRequest) AdminMarketplaceListResponse {
	response := AdminMarketplaceListResponse{
		View: adminMarketplaceView(composed.viewer), Models: []AdminMarketplaceModelDTO{},
		Filters: adminMarketplaceFilters(composed.real),
	}
	userLikeRequest := ListRequest{Search: req.Search, Provider: req.Provider, Kind: req.Kind}
	for _, model := range composed.real {
		if matchesRealListRequest(model, userLikeRequest) {
			response.Models = append(response.Models, mapAdminRealModel(model))
		}
	}
	for _, model := range composed.routing {
		if matchesRoutingListRequest(model, userLikeRequest) {
			response.Models = append(response.Models, mapAdminRoutingModel(model))
		}
	}
	sort.Slice(response.Models, func(i, j int) bool {
		left := adminMarketplaceModelName(response.Models[i])
		right := adminMarketplaceModelName(response.Models[j])
		if left != right {
			return left < right
		}
		return response.Models[i].Kind < response.Models[j].Kind
	})
	return response
}

func adminMarketplaceView(viewer MarketplaceViewer) AdminMarketplaceViewDTO {
	if viewer.AdminGlobal {
		return AdminMarketplaceViewDTO{Mode: AdminModeGlobal, SelectedToken: nil}
	}
	selected := &AdminSelectedTokenDTO{}
	if viewer.Token != nil {
		selected.ID = viewer.Token.ID
		selected.Name = viewer.Token.Name
	}
	return AdminMarketplaceViewDTO{Mode: AdminModeTokenPreview, SelectedToken: selected}
}

func adminMarketplaceFilters(models []composedRealModel) AdminMarketplaceFiltersDTO {
	userFilters := userMarketplaceFilters(models)
	return AdminMarketplaceFiltersDTO{
		Providers: stringSlice(userFilters.Providers), InputModalities: stringSlice(userFilters.InputModalities),
		OutputModalities: stringSlice(userFilters.OutputModalities),
	}
}

func mapAdminRealModel(model composedRealModel) AdminMarketplaceModelDTO {
	offers := make([]AdminModelOfferDTO, 0, len(model.offers))
	for _, offer := range model.offers {
		offers = append(offers, mapAdminOffer(offer, model.performance))
	}
	return AdminMarketplaceModelDTO{Kind: ModelKindReal, Real: &AdminRealModelDTO{
		ModelName: model.model.ModelName, Metadata: mapAdminMetadata(model.model.Metadata),
		AggregateStatus:     model.performance.ModelStatus,
		Performance:         mapAdminModelPerformance(model.performance.Model),
		AvailableOfferCount: model.model.AvailableOfferCount, PlatformOfferCount: model.model.PlatformOfferCount,
		PrivateOfferCount: model.model.PrivateOfferCount, Offers: offers,
	}}
}

func mapAdminModelPerformance(performance ModelPerformanceSnapshot) AdminModelPerformanceDTO {
	history := make([]AdminModelPerformanceStatusDTO, 0, len(performance.StatusBuckets))
	for _, bucket := range performance.StatusBuckets {
		history = append(history, AdminModelPerformanceStatusDTO{
			StartedAt: bucket.StartedAt, EndedAt: bucket.EndedAt, SuccessRate: bucket.SuccessRate,
			Status: bucket.Status, InProgress: bucket.InProgress,
		})
	}
	return AdminModelPerformanceDTO{
		PerformanceStatus: performance.PerformanceStatus,
		Window:            performance.Window, SuccessRate: performance.SuccessRate,
		CacheHitRate: performance.CacheHitRate, StatusHistory: history,
	}
}

func mapAdminMetadata(metadata models.ModelMetadata) AdminModelMetadataDTO {
	return AdminModelMetadataDTO{
		DisplayName: metadata.DisplayName, Description: metadata.Description, Provider: metadata.Provider,
		InputModalities: stringSlice(metadata.InputModalities), OutputModalities: stringSlice(metadata.OutputModalities),
		ContextLength: metadata.ContextLength, MaxOutputTokens: metadata.MaxOutputTokens,
		SupportedParameters: stringSlice(metadata.SupportedParameters), ToolCalling: metadata.ToolCalling,
		StructuredOutput: metadata.StructuredOutput, Reasoning: metadata.Reasoning, PromptCache: metadata.PromptCache,
	}
}

func mapAdminOffer(offer composedOffer, performance ModelPerformanceResult) AdminModelOfferDTO {
	snapshot, exists := performance.Offers[offer.offer.OfferRef]
	if !exists {
		snapshot = OfferPerformanceSnapshot{
			Summary:       PerformanceSummary{Status: MarketplaceHealthUnknown},
			StatusBuckets: []PerformanceStatusBucket{}, Trend: []PerformanceTrendPoint{},
		}
	}
	statusHistory := make([]AdminPerformanceStatusDTO, 0, len(snapshot.StatusBuckets))
	for _, point := range snapshot.StatusBuckets {
		statusHistory = append(statusHistory, AdminPerformanceStatusDTO{
			StartedAt: point.StartedAt, EndedAt: point.EndedAt, Status: point.Status, InProgress: point.InProgress,
		})
	}
	trend := make([]AdminPerformanceTrendDTO, 0, len(snapshot.Trend))
	for _, point := range snapshot.Trend {
		trend = append(trend, AdminPerformanceTrendDTO{
			StartedAt: point.StartedAt, EndedAt: point.EndedAt, Status: point.Status, InProgress: point.InProgress,
			SuccessRate: point.SuccessRate, TTFTAvgMs: point.TTFTAvgMs, TPSAvg: point.TPSAvg,
			TokenUnits: mapAdminTokenUnits(point.TokenUnits),
		})
	}
	usage := make([]AdminOfferUsageReferenceDTO, 0, len(offer.usage))
	for _, reference := range offer.usage {
		usage = append(usage, AdminOfferUsageReferenceDTO{
			Scope: reference.Scope, Window: reference.Window, TokenUnits: mapAdminTokenUnits(reference.TokenUnits),
			ReferenceCost: reference.ReferenceCost, GatewayChargeCost: reference.GatewayChargeCost,
			EstimatedTotalCost: reference.EstimatedTotalCost, Accuracy: reference.Accuracy,
			IncludesSharedUsage: reference.IncludesSharedUsage,
		})
	}
	return AdminModelOfferDTO{
		OfferRef: offer.offer.OfferRef, Kind: offer.offer.Kind, DisplayName: offer.offer.DisplayName,
		Ownership: offer.offer.Ownership, Available: offer.offer.Available,
		SupportedEndpoints: supportedEndpointSlice(offer.offer.SupportedEndpoints),
		Pricing:            mapAdminPricing(offer.pricing), PerformanceStatus: performance.PerformanceStatus,
		Performance: mapAdminPerformanceSummary(snapshot.Summary, snapshot.Diagnostics), StatusHistory: statusHistory,
		TrendSeries: trend, UsageReferences: usage, Diagnostics: mapAdminDiagnostics(offer.offer),
	}
}

func mapAdminDiagnostics(offer ModelOffer) AdminOfferDiagnosticsDTO {
	paths := make([]AdminEndpointPathDTO, 0, len(offer.Facts.EndpointPaths))
	for endpoint, path := range offer.Facts.EndpointPaths {
		paths = append(paths, AdminEndpointPathDTO{Endpoint: endpoint, Path: path})
	}
	sort.Slice(paths, func(i, j int) bool { return paths[i].Endpoint < paths[j].Endpoint })
	diagnostics := AdminOfferDiagnosticsDTO{
		InternalName: offer.Facts.InternalName, PublicDisplayName: offer.Facts.PublicDisplayName,
		OwnerID: offer.Facts.OwnerID, BaseURL: offer.Facts.BaseURL,
		EndpointPaths: paths, DisabledReasons: stringSlice(offer.Facts.DisabledReasons),
	}
	if offer.Kind == OfferKindPlatform {
		diagnostics.ChannelID = offer.Identity.SourceID
	} else {
		diagnostics.PrivateChannelID = offer.Identity.SourceID
	}
	return diagnostics
}

func mapAdminPricing(pricing OfferPricing) AdminOfferPricingDTO {
	return AdminOfferPricingDTO{
		ReferencePrice: mapAdminPrices(pricing.ReferencePrice), GatewayCharge: mapAdminPrices(pricing.GatewayCharge),
		EstimatedTotal: mapAdminPrices(pricing.EstimatedTotal), Accuracy: pricing.Accuracy,
	}
}

func mapAdminPrices(prices ModelPrices) AdminModelPricesDTO {
	return AdminModelPricesDTO{Input: prices.Input, Output: prices.Output, CacheRead: prices.CacheRead, CacheWrite: prices.CacheWrite}
}

func mapAdminPerformanceSummary(summary PerformanceSummary, diagnostics PerformanceDiagnostics) AdminPerformanceSummaryDTO {
	return AdminPerformanceSummaryDTO{
		Status: summary.Status, SuccessRate: summary.SuccessRate, TTFTAvgMs: summary.TTFTAvgMs,
		TTFTP95Ms: summary.TTFTP95Ms, TPSAvg: summary.TPSAvg, TPSP5: summary.TPSP5,
		DurationP95Ms: summary.DurationP95Ms, TokenUnits: mapAdminTokenUnits(summary.TokenUnits),
		RequestCount: diagnostics.RequestCount, SuccessCount: diagnostics.SuccessCount,
		FailureCount: diagnostics.FailureCount, StreamRequestCount: diagnostics.StreamRequestCount,
		TTFTSampleCount: diagnostics.TTFTSampleCount, TPSSampleCount: diagnostics.TPSSampleCount,
		DurationSampleCount: diagnostics.DurationSampleCount,
	}
}

func mapAdminTokenUnits(units TokenUnits) AdminTokenUnitsDTO {
	return AdminTokenUnitsDTO{
		Input: units.Input, Output: units.Output, CacheRead: units.CacheRead, CacheWrite: units.CacheWrite, Total: units.Total,
	}
}

func mapAdminRoutingModel(model RoutingModel) AdminMarketplaceModelDTO {
	destinations := make([]AdminRoutingDestinationDTO, 0, len(model.FlattenedDestinations))
	for _, destination := range model.FlattenedDestinations {
		offers := make([]AdminRoutingOfferDTO, 0, len(destination.Offers))
		for _, offer := range destination.Offers {
			offers = append(offers, AdminRoutingOfferDTO{
				OfferRef: offer.OfferRef, Kind: offer.Kind, DisplayName: offer.DisplayName,
				Ownership: offer.Ownership, Available: offer.Available,
				SupportedEndpoints: supportedEndpointSlice(offer.SupportedEndpoints),
			})
		}
		destinations = append(destinations, AdminRoutingDestinationDTO{ModelName: destination.ModelName, Offers: offers})
	}
	return AdminMarketplaceModelDTO{Kind: ModelKindRouting, Routing: &AdminRoutingModelDTO{
		ModelName: model.ModelName, DisplayName: model.DisplayName,
		ReachableRealModels: stringSlice(model.ReachableRealModels), FlattenedDestinations: destinations,
		RoutingWarnings: routingWarningSlice(model.RoutingWarnings), Guidance: model.Guidance,
		Diagnostics: mapAdminRoutingDiagnostics(model.Facts),
	}}
}

func mapAdminRoutingDiagnostics(facts RoutingModelFacts) AdminRoutingDiagnosticsDTO {
	definitions := make([]AdminRoutingDefinitionDTO, 0, len(facts.Definitions))
	for _, definition := range facts.Definitions {
		members := make([]AdminRoutingMemberDTO, 0, len(definition.Members))
		for _, member := range definition.Members {
			members = append(members, AdminRoutingMemberDTO{
				Ref: member.Ref, Priority: member.Priority, Weight: member.Weight,
				Kind: member.Kind, ModelName: member.ModelName, RoutingID: member.RoutingID,
			})
		}
		definitions = append(definitions, AdminRoutingDefinitionDTO{
			OccurrenceID: definition.OccurrenceID, Path: mapAdminRoutingPath(definition.Path),
			RoutingID: definition.RoutingID, Name: definition.Name, Scope: definition.Scope,
			UserID: definition.UserID, TokenID: definition.TokenID, Enabled: definition.Enabled,
			Members: members,
		})
	}
	return AdminRoutingDiagnosticsDTO{Definitions: definitions}
}

func mapAdminRoutingPath(path []RoutingPathStepFact) []AdminRoutingPathDTO {
	result := make([]AdminRoutingPathDTO, 0, len(path))
	for _, step := range path {
		result = append(result, AdminRoutingPathDTO{Ref: step.Ref, RoutingID: step.RoutingID})
	}
	return result
}

func adminMarketplaceModelName(model AdminMarketplaceModelDTO) string {
	if model.Real != nil {
		return model.Real.ModelName
	}
	if model.Routing != nil {
		return model.Routing.ModelName
	}
	return ""
}
