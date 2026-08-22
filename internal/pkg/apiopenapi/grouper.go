package apiopenapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
)

const routeSlugExtension = "x-ai-gateway-route-slug"

var routeSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._~-]*$`)

// RouteGroupChoice assigns complete OpenAPI paths to one confirmed Route slug.
// Paths omitted from choices keep their document-derived grouping.
type RouteGroupChoice struct {
	Slug  string   `json:"slug"`
	Paths []string `json:"paths"`
}

// ImportRoute is a platform APIRoute draft plus the public path produced by
// its slug. PublicPaths is keyed by the original OpenAPI path.
type ImportRoute struct {
	models.APIRoute
	PublicPaths map[string]string `json:"public_paths"`
}

// ImportBundle contains previewable, persistence-ready service document and
// Route drafts. Persistence and upstream selection belong to later layers.
type ImportBundle struct {
	Name        string                        `json:"name"`
	Description string                        `json:"description"`
	Document    models.OpenAPIServiceDocument `json:"document"`
	Servers     []models.OpenAPIServer        `json:"servers"`
	Routes      []ImportRoute                 `json:"routes"`
	Warnings    []Problem                     `json:"warnings"`
}

// RouteSlugs returns stable lexical ordering, including an empty root slug.
func (b ImportBundle) RouteSlugs() []string {
	slugs := make([]string, 0, len(b.Routes))
	for _, route := range b.Routes {
		slugs = append(slugs, route.Slug)
	}
	sort.Strings(slugs)
	return slugs
}

// Route finds a grouped Route by slug.
func (b *ImportBundle) Route(slug string) *ImportRoute {
	if b == nil {
		return nil
	}
	for index := range b.Routes {
		if b.Routes[index].Slug == slug {
			return &b.Routes[index]
		}
	}
	return nil
}

// GroupRoutes groups paths by their first static segment, then applies any
// explicit preview choices. It does not interpret path-template parameters.
func GroupRoutes(document ParsedDocument, choices []RouteGroupChoice) (ImportBundle, error) {
	effectivePaths, skippedWarnings, err := effectiveRoutePaths(document)
	if err != nil {
		return ImportBundle{}, err
	}
	assignments, err := routeAssignments(document.Paths, effectivePaths, choices)
	if err != nil {
		return ImportBundle{}, err
	}
	routes, warnings, err := buildImportRoutes(document.Paths, effectivePaths, assignments)
	if err != nil {
		return ImportBundle{}, err
	}
	warnings = append(skippedWarnings, warnings...)
	if len(document.Document.Servers) == 0 {
		warnings = append(warnings, Problem{
			Path: "$.servers", Code: "missing_server",
			Message: "document has no server; an upstream URL must be supplied during import",
		})
	}
	return ImportBundle{
		Name: document.Name, Description: document.Description, Document: document.Document,
		Servers: append([]models.OpenAPIServer(nil), document.Document.Servers...), Routes: routes, Warnings: warnings,
	}, nil
}

func routeAssignments(allPaths, effectivePaths map[string]models.OpenAPIPathItem, choices []RouteGroupChoice) (map[string]string, error) {
	assignments := make(map[string]string, len(effectivePaths))
	chosen := make(map[string]struct{})
	for choiceIndex, choice := range choices {
		if err := validateRouteSlug(choice.Slug, fmt.Sprintf("$.choices[%d].slug", choiceIndex)); err != nil {
			return nil, err
		}
		for pathIndex, path := range choice.Paths {
			choicePath := fmt.Sprintf("$.choices[%d].paths[%d]", choiceIndex, pathIndex)
			if _, exists := allPaths[path]; !exists {
				return nil, problemError(choicePath, "unknown_path", "choice refers to a path that is not in the document")
			}
			if _, duplicate := chosen[path]; duplicate {
				return nil, problemError(choicePath, "duplicate_path_choice", "path is assigned by more than one choice")
			}
			chosen[path] = struct{}{}
			if _, routable := effectivePaths[path]; routable {
				assignments[path] = choice.Slug
			}
		}
	}
	for _, path := range sortedPathNames(effectivePaths) {
		if _, exists := chosen[path]; exists {
			continue
		}
		slug, err := documentRouteSlug(path, effectivePaths[path])
		if err != nil {
			return nil, err
		}
		assignments[path] = slug
	}
	return assignments, nil
}

func effectiveRoutePaths(document ParsedDocument) (map[string]models.OpenAPIPathItem, []Problem, error) {
	effective := make(map[string]models.OpenAPIPathItem, len(document.Paths))
	var warnings []Problem
	for _, path := range sortedPathNames(document.Paths) {
		item, err := resolveEffectivePathItem(document, document.Paths[path], make(map[string]struct{}))
		if err != nil {
			return nil, nil, err
		}
		if len(item.Operations) == 0 {
			warnings = append(warnings, Problem{
				Path: appendJSONPath("$.paths", path), Code: "path_without_operations",
				Message: "path has no supported operations and was skipped",
			})
			continue
		}
		effective[path] = item
	}
	return effective, warnings, nil
}

func resolveEffectivePathItem(document ParsedDocument, item models.OpenAPIPathItem, visiting map[string]struct{}) (models.OpenAPIPathItem, error) {
	if item.Ref == "" {
		return item, nil
	}
	target, identity, ok := findPathItemReference(document, item.Ref)
	if !ok {
		return models.OpenAPIPathItem{}, problemError("$", "invalid_reference", "path item reference does not exist")
	}
	if _, cycle := visiting[identity]; cycle {
		return models.OpenAPIPathItem{}, problemError("$", "circular_reference", "circular references are not supported")
	}
	visiting[identity] = struct{}{}
	referenced, err := resolveEffectivePathItem(document, target, visiting)
	delete(visiting, identity)
	if err != nil {
		return models.OpenAPIPathItem{}, err
	}
	return mergePathItems(referenced, item), nil
}

func findPathItemReference(document ParsedDocument, reference string) (models.OpenAPIPathItem, string, bool) {
	if !strings.HasPrefix(reference, "#") {
		return models.OpenAPIPathItem{}, "", false
	}
	fragment, err := url.PathUnescape(strings.TrimPrefix(reference, "#"))
	if err != nil || !strings.HasPrefix(fragment, "/") {
		return models.OpenAPIPathItem{}, "", false
	}
	encodedSegments := strings.Split(strings.TrimPrefix(fragment, "/"), "/")
	segments := make([]string, 0, len(encodedSegments))
	for _, encoded := range encodedSegments {
		segment, valid := decodeJSONPointerSegment(encoded)
		if !valid {
			return models.OpenAPIPathItem{}, "", false
		}
		segments = append(segments, segment)
	}
	if len(segments) == 3 && segments[0] == "components" && segments[1] == "pathItems" {
		item, exists := document.Document.Components.PathItems[segments[2]]
		return item, "components/pathItems/" + segments[2], exists
	}
	if len(segments) == 2 && segments[0] == "paths" {
		item, exists := document.Paths[segments[1]]
		return item, "paths/" + segments[1], exists
	}
	return models.OpenAPIPathItem{}, "", false
}

func mergePathItems(referenced, local models.OpenAPIPathItem) models.OpenAPIPathItem {
	merged := referenced
	merged.Ref = local.Ref
	if local.Summary != "" {
		merged.Summary = local.Summary
	}
	if local.Description != "" {
		merged.Description = local.Description
	}
	if local.Parameters != nil {
		merged.Parameters = local.Parameters
	}
	if local.Servers != nil {
		merged.Servers = local.Servers
	}
	merged.Operations = mergeOperations(referenced.Operations, local.Operations)
	merged.Extensions = mergeExtensions(referenced.Extensions, local.Extensions)
	return merged
}

func mergeOperations(referenced, local map[string]models.OpenAPIOperation) map[string]models.OpenAPIOperation {
	merged := make(map[string]models.OpenAPIOperation, len(referenced)+len(local))
	for method, operation := range referenced {
		merged[method] = operation
	}
	for method, operation := range local {
		merged[method] = operation
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func mergeExtensions(referenced, local map[string]json.RawMessage) map[string]json.RawMessage {
	merged := make(map[string]json.RawMessage, len(referenced)+len(local))
	for name, value := range referenced {
		merged[name] = value
	}
	for name, value := range local {
		merged[name] = value
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func documentRouteSlug(path string, item models.OpenAPIPathItem) (string, error) {
	if raw, ok := item.Extensions[routeSlugExtension]; ok {
		var slug string
		if err := json.Unmarshal(raw, &slug); err != nil {
			return "", problemError(appendJSONPath(appendJSONPath("$.paths", path), routeSlugExtension), "invalid_route_slug", "route slug extension must be a string")
		}
		if err := validateRouteSlug(slug, appendJSONPath(appendJSONPath("$.paths", path), routeSlugExtension)); err != nil {
			return "", err
		}
		return slug, nil
	}
	slug, _ := staticPathPrefix(path)
	if err := validateRouteSlug(slug, appendJSONPath("$.paths", path)); err != nil {
		return "", err
	}
	return slug, nil
}

func validateRouteSlug(slug, path string) error {
	if slug == "" {
		return nil
	}
	if !routeSlugPattern.MatchString(slug) || len(slug) > 64 {
		return problemError(path, "invalid_route_slug", "route slug must be lowercase and URL-safe")
	}
	return nil
}

type routeAccumulator struct {
	slug          string
	upstreamPath  string
	methods       map[string]struct{}
	paths         map[string]models.OpenAPIPathItem
	publicPaths   map[string]string
	publicChanged []Problem
}

func buildImportRoutes(storedPaths, effectivePaths map[string]models.OpenAPIPathItem, assignments map[string]string) ([]ImportRoute, []Problem, error) {
	groups := make(map[string]*routeAccumulator)
	for _, path := range sortedPathNames(effectivePaths) {
		slug := assignments[path]
		_, upstreamPath := staticPathPrefix(path)
		if slug == "" {
			upstreamPath = ""
		}
		group, exists := groups[slug]
		if !exists {
			group = &routeAccumulator{
				slug: slug, upstreamPath: upstreamPath, methods: make(map[string]struct{}),
				paths: make(map[string]models.OpenAPIPathItem), publicPaths: make(map[string]string),
			}
			groups[slug] = group
		} else if group.upstreamPath != upstreamPath {
			return nil, nil, problemError(appendJSONPath("$.paths", path), "route_slug_conflict", "one route slug cannot map multiple upstream path prefixes")
		}
		if err := group.addPath(path, storedPaths[path], effectivePaths[path]); err != nil {
			return nil, nil, err
		}
	}

	slugs := make([]string, 0, len(groups))
	for slug := range groups {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	routes := make([]ImportRoute, 0, len(slugs))
	var warnings []Problem
	for _, slug := range slugs {
		group := groups[slug]
		methods := sortedSet(group.methods)
		routes = append(routes, ImportRoute{
			APIRoute: models.APIRoute{
				Slug: slug, Protocols: datatypes.JSONSlice[models.APIProtocol]{models.APIProtocolHTTP},
				AllowedMethods: datatypes.JSONSlice[string](methods), UpstreamPath: group.upstreamPath,
				ForwardSubpath: true, Status: consts.StatusEnabled,
				OpenAPIPaths: datatypes.NewJSONType(group.paths),
			},
			PublicPaths: group.publicPaths,
		})
		warnings = append(warnings, group.publicChanged...)
	}
	return routes, warnings, nil
}

func (group *routeAccumulator) addPath(path string, stored, effective models.OpenAPIPathItem) error {
	for method := range effective.Operations {
		normalized, _, valid := normalizeOperationMethod(method)
		if !valid {
			return problemError(appendJSONPath(appendJSONPath("$.paths", path), method), "invalid_method", "operation method is not supported")
		}
		group.methods[normalized] = struct{}{}
	}
	group.paths[path] = stored
	publicPath := publicPathForRoute(path, group.upstreamPath, group.slug)
	group.publicPaths[path] = publicPath
	if publicPath != path {
		group.publicChanged = append(group.publicChanged, Problem{
			Path: appendJSONPath("$.paths", path), Code: "public_path_changed",
			Message: fmt.Sprintf("public path changes from %s to %s", path, publicPath),
		})
	}
	return nil
}

func staticPathPrefix(path string) (string, string) {
	trimmed := strings.TrimPrefix(path, "/")
	if trimmed == "" {
		return "", ""
	}
	first, _, _ := strings.Cut(trimmed, "/")
	if strings.ContainsAny(first, "{}") {
		return "", ""
	}
	return first, "/" + first
}

func publicPathForRoute(path, upstreamPath, slug string) string {
	if slug == "" {
		return path
	}
	suffix := strings.TrimPrefix(path, upstreamPath)
	return "/" + slug + suffix
}

func sortedPathNames(paths map[string]models.OpenAPIPathItem) []string {
	names := make([]string, 0, len(paths))
	for path := range paths {
		names = append(names, path)
	}
	sort.Strings(names)
	return names
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
