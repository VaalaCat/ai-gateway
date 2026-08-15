package model_marketplace

import (
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type MarketplaceModelKind string

const (
	ModelKindReal    MarketplaceModelKind = "real"
	ModelKindRouting MarketplaceModelKind = "routing"
)

type ListRequest struct {
	api.PaginationQuery
	TokenID  uint                 `form:"token_id"`
	Search   string               `form:"search"`
	Provider string               `form:"provider"`
	Kind     MarketplaceModelKind `form:"kind"`
}

type UserSelectedTokenDTO struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

type UserMarketplaceFiltersDTO struct {
	Providers        []string `json:"providers"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

type UserMarketplaceListResponse struct {
	SelectedToken UserSelectedTokenDTO      `json:"selected_token"`
	Models        []UserMarketplaceModelDTO `json:"models"`
	Filters       UserMarketplaceFiltersDTO `json:"filters"`
	Total         int64                     `json:"total"`
	Page          int                       `json:"page"`
	PageSize      int                       `json:"page_size"`
}

// UserMarketplaceModelDTO is a product-only tagged union. Omitting the unused
// arm is shape control, not a security boundary: both arms are dedicated
// ordinary-user allowlists and contain no internal domain structs.
type UserMarketplaceModelDTO struct {
	Kind    MarketplaceModelKind `json:"kind"`
	Real    *UserRealModelDTO    `json:"real,omitempty"`
	Routing *UserRoutingModelDTO `json:"routing,omitempty"`
}

type UserRealModelDTO struct {
	ModelName           string                  `json:"model_name"`
	Metadata            UserModelMetadataDTO    `json:"metadata"`
	AggregateStatus     MarketplaceHealthStatus `json:"aggregate_status"`
	Performance         UserModelPerformanceDTO `json:"performance"`
	AvailableOfferCount int                     `json:"available_offer_count"`
	PlatformOfferCount  int                     `json:"platform_offer_count"`
	PrivateOfferCount   int                     `json:"private_offer_count"`
	Offers              []UserModelOfferDTO     `json:"offers"`
}

type UserModelPerformanceDTO struct {
	PerformanceStatus PerformanceStatus               `json:"performance_status"`
	Window            UsageWindow                     `json:"window"`
	SuccessRate       *float64                        `json:"success_rate"`
	CacheHitRate      *float64                        `json:"cache_hit_rate"`
	StatusHistory     []UserModelPerformanceStatusDTO `json:"status_history"`
}

type UserModelPerformanceStatusDTO struct {
	StartedAt   int64                   `json:"started_at"`
	EndedAt     int64                   `json:"ended_at"`
	SuccessRate *float64                `json:"success_rate"`
	Status      MarketplaceHealthStatus `json:"status"`
	InProgress  bool                    `json:"in_progress"`
}

type UserModelMetadataDTO struct {
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

type UserModelOfferDTO struct {
	OfferRef           string                       `json:"offer_ref"`
	Kind               ModelOfferKind               `json:"kind"`
	DisplayName        string                       `json:"display_name"`
	Ownership          ModelOfferOwnership          `json:"ownership"`
	Available          bool                         `json:"available"`
	SupportedEndpoints []SupportedEndpoint          `json:"supported_endpoints"`
	Pricing            UserOfferPricingDTO          `json:"pricing"`
	PerformanceStatus  PerformanceStatus            `json:"performance_status"`
	Performance        UserPerformanceSummaryDTO    `json:"performance"`
	StatusHistory      []UserPerformanceStatusDTO   `json:"status_history"`
	TrendSeries        []UserPerformanceTrendDTO    `json:"trend_series"`
	UsageReferences    []UserOfferUsageReferenceDTO `json:"usage_references"`
}

type UserModelPricesDTO struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

type UserOfferPricingDTO struct {
	ReferencePrice UserModelPricesDTO `json:"reference_price"`
	GatewayCharge  UserModelPricesDTO `json:"gateway_charge"`
	EstimatedTotal UserModelPricesDTO `json:"estimated_total"`
	Accuracy       OfferAccuracy      `json:"accuracy"`
}

type UserTokenUnitsDTO struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Total      int64 `json:"total"`
}

type UserPerformanceSummaryDTO struct {
	Status        MarketplaceHealthStatus `json:"status"`
	SuccessRate   *float64                `json:"success_rate"`
	TTFTAvgMs     *float64                `json:"ttft_avg_ms"`
	TTFTP95Ms     *int64                  `json:"ttft_p95_ms"`
	TPSAvg        *float64                `json:"tps_avg"`
	TPSP5         *float64                `json:"tps_p5"`
	DurationP95Ms *int64                  `json:"duration_p95_ms"`
	TokenUnits    UserTokenUnitsDTO       `json:"token_units"`
}

type UserPerformanceStatusDTO struct {
	StartedAt  int64                   `json:"started_at"`
	EndedAt    int64                   `json:"ended_at"`
	Status     MarketplaceHealthStatus `json:"status"`
	InProgress bool                    `json:"in_progress"`
}

type UserPerformanceTrendDTO struct {
	StartedAt   int64                   `json:"started_at"`
	EndedAt     int64                   `json:"ended_at"`
	Status      MarketplaceHealthStatus `json:"status"`
	InProgress  bool                    `json:"in_progress"`
	SuccessRate *float64                `json:"success_rate"`
	TTFTAvgMs   *float64                `json:"ttft_avg_ms"`
	TPSAvg      *float64                `json:"tps_avg"`
	TokenUnits  UserTokenUnitsDTO       `json:"token_units"`
}

type UserOfferUsageReferenceDTO struct {
	Scope               UsageScope        `json:"scope"`
	Window              UsageWindow       `json:"window"`
	TokenUnits          UserTokenUnitsDTO `json:"token_units"`
	ReferenceCost       *int64            `json:"reference_cost"`
	GatewayChargeCost   int64             `json:"gateway_charge_cost"`
	EstimatedTotalCost  *int64            `json:"estimated_total_cost"`
	Accuracy            OfferAccuracy     `json:"accuracy"`
	IncludesSharedUsage bool              `json:"includes_shared_usage"`
}

type UserRoutingModelDTO struct {
	ModelName             string                      `json:"model_name"`
	DisplayName           string                      `json:"display_name"`
	ReachableRealModels   []string                    `json:"reachable_real_models"`
	FlattenedDestinations []UserRoutingDestinationDTO `json:"flattened_destinations"`
	RoutingWarnings       []RoutingWarning            `json:"routing_warnings"`
	Guidance              RoutingModelGuidance        `json:"guidance"`
}

type UserRoutingDestinationDTO struct {
	ModelName string                `json:"model_name"`
	Offers    []UserRoutingOfferDTO `json:"offers"`
}

type UserRoutingOfferDTO struct {
	OfferRef           string              `json:"offer_ref"`
	Kind               ModelOfferKind      `json:"kind"`
	DisplayName        string              `json:"display_name"`
	Ownership          ModelOfferOwnership `json:"ownership"`
	Available          bool                `json:"available"`
	SupportedEndpoints []SupportedEndpoint `json:"supported_endpoints"`
}

func (h *Handler) List(c *app.Context, req ListRequest) (UserMarketplaceListResponse, error) {
	viewer, err := h.gate.RequireUser(c, req.TokenID)
	if err != nil {
		return UserMarketplaceListResponse{}, err
	}
	if err := validateListKind(req.Kind); err != nil {
		return UserMarketplaceListResponse{}, err
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	directory, err := h.buildMarketplaceDirectory(c.RequestContext(), viewer, false)
	if err != nil {
		return UserMarketplaceListResponse{}, err
	}
	filters := userMarketplaceFilters(directory.real)
	pageData, total := filterAndPageMarketplaceDirectory(directory, req, page, pageSize)
	pageData, err = h.enrichMarketplacePerformance(c.RequestContext(), pageData, UsageWindow24Hours)
	if err != nil {
		return UserMarketplaceListResponse{}, err
	}
	return mapUserList(pageData, filters, total, page, pageSize), nil
}

func validateListKind(kind MarketplaceModelKind) error {
	if kind == "" || kind == ModelKindReal || kind == ModelKindRouting {
		return nil
	}
	return api.BadRequestError("invalid marketplace model kind", nil)
}

func mapUserList(
	pageData composedMarketplace,
	filters UserMarketplaceFiltersDTO,
	total int64,
	page int,
	pageSize int,
) UserMarketplaceListResponse {
	response := UserMarketplaceListResponse{
		SelectedToken: mapUserSelectedToken(pageData.viewer),
		Models:        []UserMarketplaceModelDTO{},
		Filters:       filters,
		Total:         total,
		Page:          page,
		PageSize:      pageSize,
	}
	for _, model := range pageData.real {
		response.Models = append(response.Models, mapUserRealModel(model))
	}
	for _, model := range pageData.routing {
		response.Models = append(response.Models, mapUserRoutingModel(model))
	}
	sort.Slice(response.Models, func(i, j int) bool {
		return marketplaceModelOrderLess(
			userMarketplaceModelName(response.Models[i]), response.Models[i].Kind,
			userMarketplaceModelName(response.Models[j]), response.Models[j].Kind,
		)
	})
	return response
}

func mapUserSelectedToken(viewer MarketplaceViewer) UserSelectedTokenDTO {
	if viewer.Token == nil {
		return UserSelectedTokenDTO{}
	}
	return UserSelectedTokenDTO{ID: viewer.Token.ID, Name: viewer.Token.Name}
}

func userMarketplaceFilters(models []composedRealModel) UserMarketplaceFiltersDTO {
	providers := make(map[string]struct{})
	inputs := make(map[string]struct{})
	outputs := make(map[string]struct{})
	for _, model := range models {
		addFilterValue(providers, model.model.Metadata.Provider)
		for _, value := range model.model.Metadata.InputModalities {
			addFilterValue(inputs, value)
		}
		for _, value := range model.model.Metadata.OutputModalities {
			addFilterValue(outputs, value)
		}
	}
	return UserMarketplaceFiltersDTO{
		Providers: sortedFilterValues(providers), InputModalities: sortedFilterValues(inputs),
		OutputModalities: sortedFilterValues(outputs),
	}
}

func addFilterValue(values map[string]struct{}, value string) {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		values[trimmed] = struct{}{}
	}
}

func sortedFilterValues(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func matchesRealListRequest(model composedRealModel, req ListRequest) bool {
	if req.Kind == ModelKindRouting {
		return false
	}
	if provider := strings.TrimSpace(req.Provider); provider != "" &&
		!strings.EqualFold(provider, model.model.Metadata.Provider) {
		return false
	}
	return containsMarketplaceSearch(req.Search, model.model.ModelName, model.model.Metadata.DisplayName)
}

func matchesRoutingListRequest(model RoutingModel, req ListRequest) bool {
	if req.Kind == ModelKindReal || strings.TrimSpace(req.Provider) != "" {
		return false
	}
	return containsMarketplaceSearch(req.Search, model.ModelName, model.DisplayName)
}

func containsMarketplaceSearch(search string, values ...string) bool {
	search = strings.ToLower(strings.TrimSpace(search))
	if search == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), search) {
			return true
		}
	}
	return false
}

func mapUserRealModel(model composedRealModel) UserMarketplaceModelDTO {
	offers := make([]UserModelOfferDTO, 0, len(model.offers))
	for _, offer := range model.offers {
		offers = append(offers, mapUserOffer(offer, model.performance))
	}
	return UserMarketplaceModelDTO{Kind: ModelKindReal, Real: &UserRealModelDTO{
		ModelName: model.model.ModelName, Metadata: mapUserMetadata(model.model.Metadata),
		AggregateStatus:     model.performance.ModelStatus,
		Performance:         mapUserModelPerformance(model.performance.Model),
		AvailableOfferCount: model.model.AvailableOfferCount,
		PlatformOfferCount:  model.model.PlatformOfferCount,
		PrivateOfferCount:   model.model.PrivateOfferCount,
		Offers:              offers,
	}}
}

func mapUserModelPerformance(performance ModelPerformanceSnapshot) UserModelPerformanceDTO {
	history := make([]UserModelPerformanceStatusDTO, 0, len(performance.StatusBuckets))
	for _, bucket := range performance.StatusBuckets {
		history = append(history, UserModelPerformanceStatusDTO{
			StartedAt: bucket.StartedAt, EndedAt: bucket.EndedAt, SuccessRate: bucket.SuccessRate,
			Status: bucket.Status, InProgress: bucket.InProgress,
		})
	}
	return UserModelPerformanceDTO{
		PerformanceStatus: performance.PerformanceStatus,
		Window:            performance.Window, SuccessRate: performance.SuccessRate,
		CacheHitRate: performance.CacheHitRate, StatusHistory: history,
	}
}

func mapUserMetadata(metadata models.ModelMetadata) UserModelMetadataDTO {
	return UserModelMetadataDTO{
		DisplayName: metadata.DisplayName, Description: metadata.Description, Provider: metadata.Provider,
		InputModalities: stringSlice(metadata.InputModalities), OutputModalities: stringSlice(metadata.OutputModalities),
		ContextLength: metadata.ContextLength, MaxOutputTokens: metadata.MaxOutputTokens,
		SupportedParameters: stringSlice(metadata.SupportedParameters), ToolCalling: metadata.ToolCalling,
		StructuredOutput: metadata.StructuredOutput, Reasoning: metadata.Reasoning, PromptCache: metadata.PromptCache,
	}
}

func mapUserOffer(offer composedOffer, performance ModelPerformanceResult) UserModelOfferDTO {
	snapshot, exists := performance.Offers[offer.offer.OfferRef]
	if !exists {
		snapshot = OfferPerformanceSnapshot{
			Summary:       PerformanceSummary{Status: MarketplaceHealthUnknown},
			StatusBuckets: []PerformanceStatusBucket{}, Trend: []PerformanceTrendPoint{},
		}
	}
	statusHistory := make([]UserPerformanceStatusDTO, 0, len(snapshot.StatusBuckets))
	for _, point := range snapshot.StatusBuckets {
		statusHistory = append(statusHistory, UserPerformanceStatusDTO{
			StartedAt: point.StartedAt, EndedAt: point.EndedAt, Status: point.Status, InProgress: point.InProgress,
		})
	}
	trend := make([]UserPerformanceTrendDTO, 0, len(snapshot.Trend))
	for _, point := range snapshot.Trend {
		trend = append(trend, UserPerformanceTrendDTO{
			StartedAt: point.StartedAt, EndedAt: point.EndedAt, Status: point.Status, InProgress: point.InProgress,
			SuccessRate: point.SuccessRate, TTFTAvgMs: point.TTFTAvgMs, TPSAvg: point.TPSAvg,
			TokenUnits: mapUserTokenUnits(point.TokenUnits),
		})
	}
	usage := make([]UserOfferUsageReferenceDTO, 0, len(offer.usage))
	for _, reference := range offer.usage {
		usage = append(usage, UserOfferUsageReferenceDTO{
			Scope: reference.Scope, Window: reference.Window, TokenUnits: mapUserTokenUnits(reference.TokenUnits),
			ReferenceCost: reference.ReferenceCost, GatewayChargeCost: reference.GatewayChargeCost,
			EstimatedTotalCost: reference.EstimatedTotalCost, Accuracy: reference.Accuracy,
			IncludesSharedUsage: reference.IncludesSharedUsage,
		})
	}
	return UserModelOfferDTO{
		OfferRef: offer.offer.OfferRef, Kind: offer.offer.Kind, DisplayName: offer.offer.DisplayName,
		Ownership: offer.offer.Ownership, Available: offer.offer.Available,
		SupportedEndpoints: supportedEndpointSlice(offer.offer.SupportedEndpoints),
		Pricing:            mapUserPricing(offer.pricing), PerformanceStatus: performance.PerformanceStatus,
		Performance: mapUserPerformanceSummary(snapshot.Summary), StatusHistory: statusHistory,
		TrendSeries: trend, UsageReferences: usage,
	}
}

func mapUserPricing(pricing OfferPricing) UserOfferPricingDTO {
	return UserOfferPricingDTO{
		ReferencePrice: mapUserPrices(pricing.ReferencePrice), GatewayCharge: mapUserPrices(pricing.GatewayCharge),
		EstimatedTotal: mapUserPrices(pricing.EstimatedTotal), Accuracy: pricing.Accuracy,
	}
}

func mapUserPrices(prices ModelPrices) UserModelPricesDTO {
	return UserModelPricesDTO{Input: prices.Input, Output: prices.Output, CacheRead: prices.CacheRead, CacheWrite: prices.CacheWrite}
}

func mapUserPerformanceSummary(summary PerformanceSummary) UserPerformanceSummaryDTO {
	return UserPerformanceSummaryDTO{
		Status: summary.Status, SuccessRate: summary.SuccessRate, TTFTAvgMs: summary.TTFTAvgMs,
		TTFTP95Ms: summary.TTFTP95Ms, TPSAvg: summary.TPSAvg, TPSP5: summary.TPSP5,
		DurationP95Ms: summary.DurationP95Ms, TokenUnits: mapUserTokenUnits(summary.TokenUnits),
	}
}

func mapUserTokenUnits(units TokenUnits) UserTokenUnitsDTO {
	return UserTokenUnitsDTO{
		Input: units.Input, Output: units.Output, CacheRead: units.CacheRead, CacheWrite: units.CacheWrite, Total: units.Total,
	}
}

func mapUserRoutingModel(model RoutingModel) UserMarketplaceModelDTO {
	destinations := make([]UserRoutingDestinationDTO, 0, len(model.FlattenedDestinations))
	for _, destination := range model.FlattenedDestinations {
		offers := make([]UserRoutingOfferDTO, 0, len(destination.Offers))
		for _, offer := range destination.Offers {
			offers = append(offers, UserRoutingOfferDTO{
				OfferRef: offer.OfferRef, Kind: offer.Kind, DisplayName: offer.DisplayName,
				Ownership: offer.Ownership, Available: offer.Available,
				SupportedEndpoints: supportedEndpointSlice(offer.SupportedEndpoints),
			})
		}
		destinations = append(destinations, UserRoutingDestinationDTO{ModelName: destination.ModelName, Offers: offers})
	}
	return UserMarketplaceModelDTO{Kind: ModelKindRouting, Routing: &UserRoutingModelDTO{
		ModelName: model.ModelName, DisplayName: model.DisplayName,
		ReachableRealModels: stringSlice(model.ReachableRealModels), FlattenedDestinations: destinations,
		RoutingWarnings: routingWarningSlice(model.RoutingWarnings), Guidance: model.Guidance,
	}}
}

func userMarketplaceModelName(model UserMarketplaceModelDTO) string {
	if model.Real != nil {
		return model.Real.ModelName
	}
	if model.Routing != nil {
		return model.Routing.ModelName
	}
	return ""
}

func stringSlice(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func supportedEndpointSlice(values []SupportedEndpoint) []SupportedEndpoint {
	if len(values) == 0 {
		return []SupportedEndpoint{}
	}
	return append([]SupportedEndpoint(nil), values...)
}

func routingWarningSlice(values []RoutingWarning) []RoutingWarning {
	if len(values) == 0 {
		return []RoutingWarning{}
	}
	return append([]RoutingWarning(nil), values...)
}
