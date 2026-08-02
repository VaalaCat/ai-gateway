package model_marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	routingapi "github.com/VaalaCat/ai-gateway/internal/master/api/model_routing"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

type RoutingWarning string

const (
	RoutingWarningCycle          RoutingWarning = "cycle"
	RoutingWarningMaxDepth       RoutingWarning = "max_depth"
	RoutingWarningDisabled       RoutingWarning = "disabled"
	RoutingWarningModelNotFound  RoutingWarning = "model_not_found"
	RoutingWarningNoVisibleOffer RoutingWarning = "no_visible_offer"
)

type RoutingModelGuidance string

const RoutingModelGuidanceViewReachableRealModels RoutingModelGuidance = "view_reachable_real_models"

// ModelOfferSummary is a product-safe allowlist copy. It deliberately excludes
// ModelOffer.Identity even though the input offer index retains that identity
// for downstream server-side revalidation.
type ModelOfferSummary struct {
	OfferRef           string              `json:"offer_ref"`
	Kind               ModelOfferKind      `json:"kind"`
	DisplayName        string              `json:"display_name"`
	Ownership          ModelOfferOwnership `json:"ownership"`
	Available          bool                `json:"available"`
	SupportedEndpoints []SupportedEndpoint `json:"supported_endpoints"`
}

type FlattenedDestination struct {
	ModelName string              `json:"model_name"`
	Offers    []ModelOfferSummary `json:"offers"`
}

// RoutingModel has no pricing, performance, status, trend, or usage fields.
// Consumers follow Guidance to inspect the reachable real-model destinations.
type RoutingModel struct {
	ModelName             string                 `json:"model_name"`
	DisplayName           string                 `json:"display_name"`
	ReachableRealModels   []string               `json:"reachable_real_models"`
	FlattenedDestinations []FlattenedDestination `json:"flattened_destinations"`
	RoutingWarnings       []RoutingWarning       `json:"routing_warnings"`
	Guidance              RoutingModelGuidance   `json:"guidance"`
	Facts                 RoutingModelFacts      `json:"-"`
}

// RoutingModelFacts retains the already-batched administrator rule chain. It
// is not part of the ordinary routing DTO and every nested field fails closed.
type RoutingModelFacts struct {
	Definitions []RoutingDefinitionFact `json:"-"`
}

type RoutingDefinitionFact struct {
	OccurrenceID string                    `json:"-"`
	Path         []RoutingPathStepFact     `json:"-"`
	RoutingID    uint                      `json:"-"`
	Name         string                    `json:"-"`
	Scope        string                    `json:"-"`
	UserID       uint                      `json:"-"`
	TokenID      uint                      `json:"-"`
	Enabled      bool                      `json:"-"`
	Members      []RoutingMemberTargetFact `json:"-"`
}

type RoutingPathStepFact struct {
	Ref       string `json:"-"`
	RoutingID uint   `json:"-"`
}

type RoutingMemberTargetKind string

const (
	RoutingMemberTargetModel   RoutingMemberTargetKind = "model"
	RoutingMemberTargetRouting RoutingMemberTargetKind = "routing"
	RoutingMemberTargetInvalid RoutingMemberTargetKind = "invalid"
)

// RoutingMemberTargetFact is the administrator-safe result of the shared
// path-local walker decision. It contains no rule internals or credentials.
type RoutingMemberTargetFact struct {
	Ref       string                  `json:"-"`
	Priority  int                     `json:"-"`
	Weight    int                     `json:"-"`
	Kind      RoutingMemberTargetKind `json:"-"`
	ModelName string                  `json:"-"`
	RoutingID uint                    `json:"-"`
}

type RoutingModelQuery interface {
	ListMarketplaceRoutings(context.Context, dao.MarketplaceRoutingScope) ([]models.ModelRouting, error)
}

type RoutingModelFinder struct {
	query RoutingModelQuery
}

func NewRoutingModelFinder(query RoutingModelQuery) RoutingModelFinder {
	return RoutingModelFinder{query: query}
}

func (f RoutingModelFinder) Find(
	ctx context.Context,
	viewer MarketplaceViewer,
	offersByModel map[string][]ModelOffer,
) ([]RoutingModel, error) {
	if err := validateMarketplaceViewer(viewer); err != nil {
		return nil, err
	}
	if f.query == nil {
		return nil, errors.New("routing model query is required")
	}
	visibleModels, err := summarizeVisibleModelOffers(offersByModel)
	if err != nil {
		return nil, err
	}
	routings, err := f.query.ListMarketplaceRoutings(ctx, marketplaceRoutingScope(viewer))
	if err != nil {
		return nil, fmt.Errorf("find marketplace routings: %w", err)
	}
	index, err := indexMarketplaceRoutings(routings, viewer)
	if err != nil {
		return nil, err
	}
	return composeRoutingModels(index, visibleModels)
}

type visibleModelOffers struct {
	offers []ModelOfferSummary
}

type canonicalMarketplaceOffer struct {
	identity ModelOfferIdentity
	summary  ModelOfferSummary
}

func summarizeVisibleModelOffers(offersByModel map[string][]ModelOffer) (map[string]visibleModelOffers, error) {
	result := make(map[string]visibleModelOffers, len(offersByModel))
	// behavior change: offer references and identities are now a global bijection,
	// rather than being checked only inside each model bucket.
	offersByRef := make(map[string]canonicalMarketplaceOffer)
	refsByIdentity := make(map[ModelOfferIdentity]string)
	for modelName, offers := range offersByModel {
		if strings.TrimSpace(modelName) == "" || modelName != strings.TrimSpace(modelName) {
			return nil, errors.New("invalid marketplace offer model bucket")
		}
		summaries := make(map[string]ModelOfferSummary, len(offers))
		for _, offer := range offers {
			if err := validateRoutingOffer(modelName, offer); err != nil {
				return nil, err
			}
			summary := routingOfferSummary(offer)
			if err := addCanonicalMarketplaceOffer(offersByRef, refsByIdentity, offer, summary); err != nil {
				return nil, err
			}
			if !offer.Available {
				continue
			}
			summaries[summary.OfferRef] = summary
		}
		sorted := make([]ModelOfferSummary, 0, len(summaries))
		for _, summary := range summaries {
			sorted = append(sorted, summary)
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].OfferRef < sorted[j].OfferRef })
		result[modelName] = visibleModelOffers{offers: sorted}
	}
	return result, nil
}

func addCanonicalMarketplaceOffer(
	offersByRef map[string]canonicalMarketplaceOffer,
	refsByIdentity map[ModelOfferIdentity]string,
	offer ModelOffer,
	summary ModelOfferSummary,
) error {
	if previous, duplicate := offersByRef[summary.OfferRef]; duplicate {
		if previous.identity != offer.Identity || !equalModelOfferSummary(previous.summary, summary) {
			return errors.New("conflicting marketplace offer reference")
		}
	} else {
		offersByRef[summary.OfferRef] = canonicalMarketplaceOffer{identity: offer.Identity, summary: summary}
	}
	if previousRef, duplicate := refsByIdentity[offer.Identity]; duplicate && previousRef != summary.OfferRef {
		return errors.New("conflicting marketplace offer identity")
	}
	refsByIdentity[offer.Identity] = summary.OfferRef
	return nil
}

func validateRoutingOffer(modelName string, offer ModelOffer) error {
	if offer.Identity.ModelName != modelName || offer.Identity.Kind != offer.Kind || offer.Identity.SourceID == 0 {
		return errors.New("invalid marketplace offer identity")
	}
	if strings.TrimSpace(offer.OfferRef) == "" {
		return errors.New("invalid marketplace offer reference")
	}
	if offer.Kind != OfferKindPlatform && offer.Kind != OfferKindPrivate {
		return errors.New("invalid marketplace offer kind")
	}
	if offer.Available && len(offer.SupportedEndpoints) == 0 {
		return errors.New("available marketplace offer requires a supported endpoint")
	}
	return nil
}

func routingOfferSummary(offer ModelOffer) ModelOfferSummary {
	endpoints := append([]SupportedEndpoint(nil), offer.SupportedEndpoints...)
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i] < endpoints[j] })
	return ModelOfferSummary{
		OfferRef: offer.OfferRef, Kind: offer.Kind, DisplayName: offer.DisplayName,
		Ownership: offer.Ownership, Available: offer.Available, SupportedEndpoints: endpoints,
	}
}

func equalModelOfferSummary(left, right ModelOfferSummary) bool {
	return left.OfferRef == right.OfferRef &&
		left.Kind == right.Kind &&
		left.DisplayName == right.DisplayName &&
		left.Ownership == right.Ownership &&
		left.Available == right.Available &&
		slices.Equal(left.SupportedEndpoints, right.SupportedEndpoints)
}

func marketplaceRoutingScope(viewer MarketplaceViewer) dao.MarketplaceRoutingScope {
	scope := dao.MarketplaceRoutingScope{
		UserID: viewer.UserID, GroupIDs: append([]uint(nil), viewer.GroupIDs...), AdminGlobal: viewer.AdminGlobal,
	}
	if viewer.Token != nil {
		scope.TokenID = viewer.Token.ID
	}
	return scope
}

type marketplaceRoutingIndex struct {
	topLevel       []*models.ModelRouting
	globalEnabled  map[string]*models.ModelRouting
	globalDisabled map[string]*models.ModelRouting
}

func indexMarketplaceRoutings(rows []models.ModelRouting, viewer MarketplaceViewer) (marketplaceRoutingIndex, error) {
	index := marketplaceRoutingIndex{
		globalEnabled:  make(map[string]*models.ModelRouting),
		globalDisabled: make(map[string]*models.ModelRouting),
	}
	userEnabled := make(map[string]*models.ModelRouting)
	tokenEnabled := make(map[string]*models.ModelRouting)
	for i := range rows {
		route := &rows[i]
		if !marketplaceRoutingBelongsToViewer(route, viewer) {
			continue
		}
		if err := validateMarketplaceRoutingOwner(route); err != nil {
			return marketplaceRoutingIndex{}, err
		}
		switch route.Scope {
		case models.RoutingScopeGlobal:
			if err := addMarketplaceGlobalRouting(index.globalEnabled, index.globalDisabled, route); err != nil {
				return marketplaceRoutingIndex{}, err
			}
		case models.RoutingScopeUser:
			if route.Enabled {
				if err := addUniqueMarketplaceRouting(userEnabled, route); err != nil {
					return marketplaceRoutingIndex{}, err
				}
			}
		case models.RoutingScopeToken:
			if route.Enabled {
				if err := addUniqueMarketplaceRouting(tokenEnabled, route); err != nil {
					return marketplaceRoutingIndex{}, err
				}
			}
		}
	}

	if viewer.AdminGlobal {
		index.topLevel = appendGlobalRoutingDefinitions(index.globalEnabled, index.globalDisabled)
		return index, nil
	}
	// behavior change: ordinary viewers may only discover top-level aliases that
	// survive their effective token and group model whitelist.
	index.topLevel = filterMarketplaceRoutingAliases(
		effectiveMarketplaceRoutings(index.globalEnabled, userEnabled, tokenEnabled),
		viewer.AllowedModels,
	)
	return index, nil
}

func filterMarketplaceRoutingAliases(
	routes []*models.ModelRouting,
	allowedModels MarketplaceModelWhitelist,
) []*models.ModelRouting {
	visible := make([]*models.ModelRouting, 0, len(routes))
	for _, route := range routes {
		if allowedModels.Allows(route.Name) {
			visible = append(visible, route)
		}
	}
	return visible
}

func marketplaceRoutingBelongsToViewer(route *models.ModelRouting, viewer MarketplaceViewer) bool {
	switch route.Scope {
	case models.RoutingScopeGlobal:
		return true
	case models.RoutingScopeUser:
		return !viewer.AdminGlobal && viewer.UserID != 0 && route.UserID == viewer.UserID
	case models.RoutingScopeToken:
		return !viewer.AdminGlobal && viewer.Token != nil && route.TokenID == viewer.Token.ID
	default:
		return false
	}
}

func validateMarketplaceRoutingOwner(route *models.ModelRouting) error {
	if strings.TrimSpace(route.Name) == "" || route.Name != strings.TrimSpace(route.Name) {
		return errors.New("invalid marketplace routing name")
	}
	switch route.Scope {
	case models.RoutingScopeGlobal:
		if route.UserID != 0 || route.TokenID != 0 {
			return errors.New("invalid marketplace global routing owner")
		}
	case models.RoutingScopeUser:
		if route.UserID == 0 || route.TokenID != 0 {
			return errors.New("invalid marketplace user routing owner")
		}
	case models.RoutingScopeToken:
		if route.UserID != 0 || route.TokenID == 0 {
			return errors.New("invalid marketplace token routing owner")
		}
	default:
		return errors.New("invalid marketplace routing scope")
	}
	return nil
}

func addMarketplaceGlobalRouting(
	enabled map[string]*models.ModelRouting,
	disabled map[string]*models.ModelRouting,
	route *models.ModelRouting,
) error {
	if enabled[route.Name] != nil || disabled[route.Name] != nil {
		return errors.New("duplicate marketplace global routing")
	}
	if route.Enabled {
		enabled[route.Name] = route
	} else {
		disabled[route.Name] = route
	}
	return nil
}

func addUniqueMarketplaceRouting(index map[string]*models.ModelRouting, route *models.ModelRouting) error {
	if index[route.Name] != nil {
		return errors.New("duplicate marketplace scoped routing")
	}
	index[route.Name] = route
	return nil
}

func appendGlobalRoutingDefinitions(
	enabled map[string]*models.ModelRouting,
	disabled map[string]*models.ModelRouting,
) []*models.ModelRouting {
	result := make([]*models.ModelRouting, 0, len(enabled)+len(disabled))
	for _, route := range enabled {
		result = append(result, route)
	}
	for _, route := range disabled {
		result = append(result, route)
	}
	sortMarketplaceRoutings(result)
	return result
}

func effectiveMarketplaceRoutings(
	global map[string]*models.ModelRouting,
	user map[string]*models.ModelRouting,
	token map[string]*models.ModelRouting,
) []*models.ModelRouting {
	names := make(map[string]struct{}, len(global)+len(user)+len(token))
	for name := range global {
		names[name] = struct{}{}
	}
	for name := range user {
		names[name] = struct{}{}
	}
	for name := range token {
		names[name] = struct{}{}
	}
	result := make([]*models.ModelRouting, 0, len(names))
	for name := range names {
		route := token[name]
		if route == nil {
			route = user[name]
		}
		if route == nil {
			route = global[name]
		}
		result = append(result, route)
	}
	sortMarketplaceRoutings(result)
	return result
}

func sortMarketplaceRoutings(rows []*models.ModelRouting) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return routingapi.RoutingWalkKey(rows[i]) < routingapi.RoutingWalkKey(rows[j])
	})
}

type marketplaceRoutingTargetFinder struct {
	enabled    map[string]*models.ModelRouting
	disabled   map[string]*models.ModelRouting
	realModels map[string]visibleModelOffers
}

func (f marketplaceRoutingTargetFinder) FindEnabledRouting(ref string) (routingapi.RoutingWalkRoute, bool, error) {
	route, ok := f.enabled[ref]
	if !ok {
		return routingapi.RoutingWalkRoute{}, false, nil
	}
	members, err := parseMarketplaceRoutingMembers(route.Members)
	if err != nil {
		return routingapi.RoutingWalkRoute{}, false, err
	}
	return routingapi.RoutingWalkRoute{
		Key: routingapi.RoutingWalkKey(route), Scope: route.Scope, Members: members,
	}, true, nil
}

func (f marketplaceRoutingTargetFinder) FindDisabledRouting(ref string) (routingapi.RoutingWalkRoute, bool, error) {
	// behavior change: runtime omits disabled globals from its routing cache, so
	// a same-name real model remains a valid destination in marketplace walks.
	if _, realModelExists := f.realModels[ref]; realModelExists {
		return routingapi.RoutingWalkRoute{}, false, nil
	}
	route, ok := f.disabled[ref]
	if !ok {
		return routingapi.RoutingWalkRoute{}, false, nil
	}
	return routingapi.RoutingWalkRoute{Key: routingapi.RoutingWalkKey(route), Scope: route.Scope}, true, nil
}

func (f marketplaceRoutingTargetFinder) HasRealModel(ref string) bool {
	_, ok := f.realModels[ref]
	return ok
}

func parseMarketplaceRoutingMembers(raw string) ([]models.RoutingMember, error) {
	var members []models.RoutingMember
	if err := json.Unmarshal([]byte(raw), &members); err != nil {
		return nil, fmt.Errorf("invalid routing members: %w", err)
	}
	if len(members) == 0 {
		return nil, errors.New("invalid routing members: members are empty")
	}
	for _, member := range members {
		if strings.TrimSpace(member.Ref) == "" {
			return nil, errors.New("empty routing member ref")
		}
	}
	return members, nil
}

func composeRoutingModels(
	index marketplaceRoutingIndex,
	visibleModels map[string]visibleModelOffers,
) ([]RoutingModel, error) {
	targets := marketplaceRoutingTargetFinder{
		enabled: index.globalEnabled, disabled: index.globalDisabled, realModels: visibleModels,
	}
	result := make([]RoutingModel, 0, len(index.topLevel))
	for _, route := range index.topLevel {
		if !route.Enabled {
			model := newRoutingModel(route.Name, RoutingWarningDisabled)
			rootNodes, _ := walkRootRoutingMembers(route, targets)
			model.Facts = marketplaceRoutingFacts(route, index, rootNodes)
			result = append(result, model)
			continue
		}
		nodes, err := walkRootRoutingMembers(route, targets)
		if err != nil {
			return nil, err
		}
		model, err := routingModelFromWalk(route.Name, nodes, visibleModels)
		if err != nil {
			return nil, err
		}
		model.Facts = marketplaceRoutingFacts(route, index, nodes)
		result = append(result, model)
	}
	return result, nil
}

func marketplaceRoutingFacts(
	root *models.ModelRouting,
	index marketplaceRoutingIndex,
	rootNodes []routingapi.RoutingWalkNode,
) RoutingModelFacts {
	routesByKey := marketplaceRoutingRowsByKey(root, index)
	definitions := make([]RoutingDefinitionFact, 0)
	rootOccurrence := routingDefinitionOccurrence{
		route:        root,
		nodes:        rootNodes,
		occurrenceID: rootRoutingOccurrenceID(root.ID),
		path:         []RoutingPathStepFact{{Ref: root.Name, RoutingID: root.ID}},
	}
	appendRoutingDefinitionOccurrences(rootOccurrence, routesByKey, &definitions)
	return RoutingModelFacts{Definitions: definitions}
}

type routingDefinitionOccurrence struct {
	route        *models.ModelRouting
	nodes        []routingapi.RoutingWalkNode
	occurrenceID string
	path         []RoutingPathStepFact
}

func appendRoutingDefinitionOccurrences(
	current routingDefinitionOccurrence,
	routesByKey map[string]*models.ModelRouting,
	definitions *[]RoutingDefinitionFact,
) {
	*definitions = append(*definitions, RoutingDefinitionFact{
		OccurrenceID: current.occurrenceID, Path: append([]RoutingPathStepFact(nil), current.path...),
		RoutingID: current.route.ID, Name: current.route.Name, Scope: current.route.Scope,
		UserID: current.route.UserID, TokenID: current.route.TokenID, Enabled: current.route.Enabled,
		Members: routingMemberTargetFacts(current.nodes, routesByKey),
	})
	if !current.route.Enabled {
		return
	}
	for ordinal, node := range current.nodes {
		if node.Kind != routingapi.RoutingWalkNodeRouting || node.RouteKey == "" {
			continue
		}
		nested := routesByKey[node.RouteKey]
		if nested == nil {
			continue
		}
		appendRoutingDefinitionOccurrences(routingDefinitionOccurrence{
			route:        nested,
			nodes:        node.Children,
			occurrenceID: nestedRoutingOccurrenceID(current.occurrenceID, ordinal, nested.ID),
			path:         appendRoutingOccurrencePath(current.path, node.Ref, nested.ID),
		}, routesByKey, definitions)
	}
}

func rootRoutingOccurrenceID(routingID uint) string {
	return fmt.Sprintf("root:%d", routingID)
}

func nestedRoutingOccurrenceID(parent string, nodeOrdinal int, routingID uint) string {
	return fmt.Sprintf("%s/%d:%d", parent, nodeOrdinal, routingID)
}

func appendRoutingOccurrencePath(
	path []RoutingPathStepFact,
	ref string,
	routingID uint,
) []RoutingPathStepFact {
	result := make([]RoutingPathStepFact, 0, len(path)+1)
	result = append(result, path...)
	return append(result, RoutingPathStepFact{Ref: ref, RoutingID: routingID})
}

func marketplaceRoutingRowsByKey(
	root *models.ModelRouting,
	index marketplaceRoutingIndex,
) map[string]*models.ModelRouting {
	rows := make(map[string]*models.ModelRouting, 1+len(index.globalEnabled)+len(index.globalDisabled))
	rows[routingapi.RoutingWalkKey(root)] = root
	for _, candidates := range []map[string]*models.ModelRouting{index.globalEnabled, index.globalDisabled} {
		for _, route := range candidates {
			rows[routingapi.RoutingWalkKey(route)] = route
		}
	}
	return rows
}

func walkRootRoutingMembers(
	route *models.ModelRouting,
	targets marketplaceRoutingTargetFinder,
) ([]routingapi.RoutingWalkNode, error) {
	members, err := parseMarketplaceRoutingMembers(route.Members)
	if err != nil {
		return nil, err
	}
	nodes, err := routingapi.WalkRoutingDestinations(routingapi.RoutingWalkRequest{
		RootRouteKey: routingapi.RoutingWalkKey(route), Members: members,
	}, targets)
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

func routingMemberTargetFacts(
	nodes []routingapi.RoutingWalkNode,
	routesByKey map[string]*models.ModelRouting,
) []RoutingMemberTargetFact {
	result := make([]RoutingMemberTargetFact, 0, len(nodes))
	for _, node := range nodes {
		fact := RoutingMemberTargetFact{
			Ref: node.Ref, Priority: node.Priority, Weight: node.Weight,
			Kind: routingMemberTargetKind(node.Kind),
		}
		if fact.Kind == RoutingMemberTargetModel {
			fact.ModelName = node.Ref
		}
		if fact.Kind == RoutingMemberTargetRouting {
			if route := routesByKey[node.RouteKey]; route != nil {
				fact.RoutingID = route.ID
			}
		}
		result = append(result, fact)
	}
	return result
}

func routingMemberTargetKind(kind routingapi.RoutingWalkNodeKind) RoutingMemberTargetKind {
	switch kind {
	case routingapi.RoutingWalkNodeModel:
		return RoutingMemberTargetModel
	case routingapi.RoutingWalkNodeRouting:
		return RoutingMemberTargetRouting
	default:
		return RoutingMemberTargetInvalid
	}
}

type safeRoutingDestinationVisitor struct {
	visibleModels map[string]visibleModelOffers
	destinations  map[string]FlattenedDestination
	warnings      map[RoutingWarning]struct{}
}

func (v *safeRoutingDestinationVisitor) Visit(nodes []routingapi.RoutingWalkNode) error {
	for _, node := range nodes {
		if err := v.visitNode(node); err != nil {
			return err
		}
	}
	return nil
}

func (v *safeRoutingDestinationVisitor) visitNode(node routingapi.RoutingWalkNode) error {
	if node.Kind == routingapi.RoutingWalkNodeModel {
		bucket, visible := v.visibleModels[node.Ref]
		if !visible || len(bucket.offers) == 0 {
			v.warnings[RoutingWarningNoVisibleOffer] = struct{}{}
		} else {
			v.destinations[node.Ref] = FlattenedDestination{
				ModelName: node.Ref, Offers: append([]ModelOfferSummary(nil), bucket.offers...),
			}
		}
	}
	if node.Issue != "" {
		warning, err := marketplaceRoutingWarning(node.Issue)
		if err != nil {
			return err
		}
		v.warnings[warning] = struct{}{}
	}
	return v.Visit(node.Children)
}

func marketplaceRoutingWarning(issue routingapi.RoutingWalkIssue) (RoutingWarning, error) {
	switch issue {
	case routingapi.RoutingWalkIssueCycle:
		return RoutingWarningCycle, nil
	case routingapi.RoutingWalkIssueMaxDepth:
		return RoutingWarningMaxDepth, nil
	case routingapi.RoutingWalkIssueDisabled:
		return RoutingWarningDisabled, nil
	case routingapi.RoutingWalkIssueNotFound:
		return RoutingWarningModelNotFound, nil
	default:
		return "", errors.New("unsupported routing walk warning")
	}
}

func routingModelFromWalk(
	name string,
	nodes []routingapi.RoutingWalkNode,
	visibleModels map[string]visibleModelOffers,
) (RoutingModel, error) {
	visitor := safeRoutingDestinationVisitor{
		visibleModels: visibleModels,
		destinations:  make(map[string]FlattenedDestination),
		warnings:      make(map[RoutingWarning]struct{}),
	}
	if err := visitor.Visit(nodes); err != nil {
		return RoutingModel{}, err
	}
	destinations := make([]FlattenedDestination, 0, len(visitor.destinations))
	for _, destination := range visitor.destinations {
		destinations = append(destinations, destination)
	}
	sort.Slice(destinations, func(i, j int) bool { return destinations[i].ModelName < destinations[j].ModelName })
	reachable := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		reachable = append(reachable, destination.ModelName)
	}
	return RoutingModel{
		ModelName: name, DisplayName: name, ReachableRealModels: reachable,
		FlattenedDestinations: destinations, RoutingWarnings: sortedRoutingWarnings(visitor.warnings),
		Guidance: RoutingModelGuidanceViewReachableRealModels,
	}, nil
}

func newRoutingModel(name string, warnings ...RoutingWarning) RoutingModel {
	warningSet := make(map[RoutingWarning]struct{}, len(warnings))
	for _, warning := range warnings {
		warningSet[warning] = struct{}{}
	}
	return RoutingModel{
		ModelName: name, DisplayName: name,
		ReachableRealModels: []string{}, FlattenedDestinations: []FlattenedDestination{},
		RoutingWarnings: sortedRoutingWarnings(warningSet), Guidance: RoutingModelGuidanceViewReachableRealModels,
	}
}

var routingWarningOrder = []RoutingWarning{
	RoutingWarningCycle,
	RoutingWarningMaxDepth,
	RoutingWarningDisabled,
	RoutingWarningModelNotFound,
	RoutingWarningNoVisibleOffer,
}

func sortedRoutingWarnings(warnings map[RoutingWarning]struct{}) []RoutingWarning {
	result := make([]RoutingWarning, 0, len(warnings))
	for _, warning := range routingWarningOrder {
		if _, ok := warnings[warning]; ok {
			result = append(result, warning)
		}
	}
	return result
}
