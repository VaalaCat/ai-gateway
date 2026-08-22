package apiopenapi

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestGroupRoutesUsesStaticFirstSegmentAndRootGroup(t *testing.T) {
	parsed, err := ParseJSON(readFixture(t, "openapi-3.0.json"))
	require.NoError(t, err)
	parsed.Paths["/orders"] = pathWithMethods("PATCH")
	parsed.Paths["/orders/{id}"] = pathWithMethods("GET", "DELETE")
	parsed.Paths["/"] = pathWithMethods("HEAD")
	parsed.Paths["/{tenant}/users"] = pathWithMethods("PUT")

	bundle, err := GroupRoutes(parsed, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"", "orders", "users"}, bundle.RouteSlugs())

	users := bundle.Route("users")
	require.NotNil(t, users)
	require.Equal(t, "/users", users.UpstreamPath)
	require.True(t, users.ForwardSubpath)
	require.Equal(t, []models.APIProtocol{models.APIProtocolHTTP}, []models.APIProtocol(users.Protocols))
	require.Equal(t, []string{"GET", "POST"}, []string(users.AllowedMethods))
	require.ElementsMatch(t, []string{"/users", "/users/{id}"}, mapKeys(users.OpenAPIPaths.Data()))
	require.Equal(t, "/users/{id}", users.PublicPaths["/users/{id}"])

	orders := bundle.Route("orders")
	require.Equal(t, "/orders", orders.UpstreamPath)
	require.Equal(t, []string{"DELETE", "GET", "PATCH"}, []string(orders.AllowedMethods))

	root := bundle.Route("")
	require.NotNil(t, root)
	require.Empty(t, root.UpstreamPath)
	require.Equal(t, []string{"HEAD", "PUT"}, []string(root.AllowedMethods))
	require.Equal(t, "/{tenant}/users", root.PublicPaths["/{tenant}/users"])
}

func TestGroupRoutesCustomChoiceReportsChangedPublicPaths(t *testing.T) {
	parsed, err := ParseJSON(readFixture(t, "openapi-3.0.json"))
	require.NoError(t, err)

	bundle, err := GroupRoutes(parsed, []RouteGroupChoice{{
		Slug:  "accounts",
		Paths: []string{"/users", "/users/{id}"},
	}})
	require.NoError(t, err)
	require.Equal(t, []string{"accounts"}, bundle.RouteSlugs())
	require.Equal(t, "/users", bundle.Route("accounts").UpstreamPath)
	require.Equal(t, "/accounts", bundle.Route("accounts").PublicPaths["/users"])
	require.Equal(t, "/accounts/{id}", bundle.Route("accounts").PublicPaths["/users/{id}"])
	require.Len(t, bundle.Warnings, 2)
	require.Equal(t, "public_path_changed", bundle.Warnings[0].Code)
	require.Contains(t, bundle.Warnings[0].Message, "/accounts")
}

func TestGroupRoutesRestoresExportedRouteSlugExtension(t *testing.T) {
	parsed := ParsedDocument{
		Name: "Round trip",
		Document: models.OpenAPIServiceDocument{
			Servers: []models.OpenAPIServer{{URL: "https://gateway.example.test"}},
		},
		Paths: map[string]models.OpenAPIPathItem{
			"/accounts/{id}": {
				Operations: map[string]models.OpenAPIOperation{"GET": {OperationID: "getAccount"}},
				Extensions: map[string]json.RawMessage{"x-ai-gateway-route-slug": json.RawMessage(`"accounts"`)},
			},
		},
	}
	bundle, err := GroupRoutes(parsed, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"accounts"}, bundle.RouteSlugs())
	require.Empty(t, bundle.Warnings)
	require.Equal(t, "/accounts", bundle.Route("accounts").UpstreamPath)
}

func TestGroupRoutesResolvesPathItemReferencesAndSkipsPathsWithoutOperations(t *testing.T) {
	parsed, err := ParseJSON([]byte(`{
      "openapi":"3.1.0","info":{"title":"API","version":"1"},
      "servers":[{"url":"https://api.example.test"}],
      "paths":{
        "/empty":{"summary":"none"},
        "/parameters-only":{"parameters":[{"name":"trace","in":"header","schema":{"type":"string"}}]},
        "/ref-empty":{"$ref":"#/components/pathItems/Empty"},
		"/users":{"$ref":"#/components/pathItems/Shared"},
		"/users/create":{"$ref":"#/components/pathItems/Shared","post":{"operationId":"createUser","responses":{"200":{"description":"ok"}}}}
      },
      "components":{"pathItems":{
        "Empty":{"description":"none"},
        "Shared":{"get":{"operationId":"listUsers","responses":{"200":{"description":"ok"}}}}
      }}
    }`))
	require.NoError(t, err)

	bundle, err := GroupRoutes(parsed, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"users"}, bundle.RouteSlugs())
	require.Equal(t, []string{"GET", "POST"}, []string(bundle.Route("users").AllowedMethods))
	storedRefOnly := bundle.Route("users").OpenAPIPaths.Data()["/users"]
	require.Equal(t, "#/components/pathItems/Shared", storedRefOnly.Ref)
	require.Nil(t, storedRefOnly.Operations, "Route must retain the original ref-only PathItem instead of a stale materialized copy")
	storedWithSibling := bundle.Route("users").OpenAPIPaths.Data()["/users/create"]
	require.Equal(t, "#/components/pathItems/Shared", storedWithSibling.Ref)
	require.NotContains(t, storedWithSibling.Operations, "GET")
	require.Equal(t, "createUser", storedWithSibling.Operations["POST"].OperationID)
	require.Equal(t, []Problem{
		{Path: `$.paths["/empty"]`, Code: "path_without_operations", Message: "path has no supported operations and was skipped"},
		{Path: `$.paths["/parameters-only"]`, Code: "path_without_operations", Message: "path has no supported operations and was skipped"},
		{Path: `$.paths["/ref-empty"]`, Code: "path_without_operations", Message: "path has no supported operations and was skipped"},
	}, bundle.Warnings)
}

func TestGroupRoutesChoicesAcceptExistingSkippedPaths(t *testing.T) {
	parsed := ParsedDocument{
		Document: models.OpenAPIServiceDocument{
			Servers: []models.OpenAPIServer{{URL: "https://api.example.test"}},
			Components: models.OpenAPIComponents{PathItems: map[string]models.OpenAPIPathItem{
				"Empty": {Description: "none"},
			}},
		},
		Paths: map[string]models.OpenAPIPathItem{
			"/users":           pathWithMethods("GET"),
			"/parameters-only": {Parameters: []models.OpenAPIParameter{{Name: "trace", In: "header"}}},
			"/ref-empty":       {Ref: "#/components/pathItems/Empty"},
		},
	}

	t.Run("valid and skipped", func(t *testing.T) {
		bundle, err := GroupRoutes(parsed, []RouteGroupChoice{{
			Slug: "users", Paths: []string{"/users", "/parameters-only", "/ref-empty"},
		}})
		require.NoError(t, err)
		require.Equal(t, []string{"users"}, bundle.RouteSlugs())
		require.Equal(t, []Problem{
			{Path: `$.paths["/parameters-only"]`, Code: "path_without_operations", Message: "path has no supported operations and was skipped"},
			{Path: `$.paths["/ref-empty"]`, Code: "path_without_operations", Message: "path has no supported operations and was skipped"},
		}, bundle.Warnings)
	})

	t.Run("all skipped", func(t *testing.T) {
		parsed.Paths = map[string]models.OpenAPIPathItem{
			"/parameters-only": {Parameters: []models.OpenAPIParameter{{Name: "trace", In: "header"}}},
			"/ref-empty":       {Ref: "#/components/pathItems/Empty"},
		}
		bundle, err := GroupRoutes(parsed, []RouteGroupChoice{{
			Slug: "ignored", Paths: []string{"/parameters-only", "/ref-empty"},
		}})
		require.NoError(t, err)
		require.Empty(t, bundle.Routes)
		require.Len(t, bundle.Warnings, 2)
	})

	t.Run("duplicate skipped", func(t *testing.T) {
		_, err := GroupRoutes(parsed, []RouteGroupChoice{
			{Slug: "first", Paths: []string{"/ref-empty"}},
			{Slug: "second", Paths: []string{"/ref-empty"}},
		})
		requireParseProblem(t, err, "$.choices[1].paths[0]", "duplicate_path_choice")
	})
}

func TestGroupRoutesAppliesChoicesBeforeDocumentSlugValidation(t *testing.T) {
	parsed := ParsedDocument{
		Document: models.OpenAPIServiceDocument{Servers: []models.OpenAPIServer{{URL: "https://api.example.test"}}},
		Paths: map[string]models.OpenAPIPathItem{
			"/Users": {
				Operations: pathWithMethods("GET").Operations,
				Extensions: map[string]json.RawMessage{routeSlugExtension: json.RawMessage(`"Not Valid"`)},
			},
		},
	}
	bundle, err := GroupRoutes(parsed, []RouteGroupChoice{{Slug: "users", Paths: []string{"/Users"}}})
	require.NoError(t, err)
	require.Equal(t, []string{"users"}, bundle.RouteSlugs())
}

func TestGroupRoutesTreatsTemplatedFirstSegmentAsRoot(t *testing.T) {
	parsed := ParsedDocument{
		Document: models.OpenAPIServiceDocument{Servers: []models.OpenAPIServer{{URL: "https://api.example.test"}}},
		Paths: map[string]models.OpenAPIPathItem{
			"/v{version}/users": pathWithMethods("GET"),
		},
	}
	bundle, err := GroupRoutes(parsed, nil)
	require.NoError(t, err)
	require.Equal(t, []string{""}, bundle.RouteSlugs())
	require.Equal(t, "/v{version}/users", bundle.Route("").PublicPaths["/v{version}/users"])
}

func TestGroupRoutesRejectsConflictingChoices(t *testing.T) {
	parsed := ParsedDocument{
		Document: models.OpenAPIServiceDocument{Servers: []models.OpenAPIServer{{URL: "https://api.example.test"}}},
		Paths: map[string]models.OpenAPIPathItem{
			"/users":  pathWithMethods("GET"),
			"/orders": pathWithMethods("POST"),
		},
	}
	tests := []struct {
		name     string
		choices  []RouteGroupChoice
		wantCode string
	}{
		{name: "same path assigned twice", choices: []RouteGroupChoice{{Slug: "users", Paths: []string{"/users"}}, {Slug: "other", Paths: []string{"/users"}}}, wantCode: "duplicate_path_choice"},
		{name: "same slug spans upstream prefixes", choices: []RouteGroupChoice{{Slug: "shared", Paths: []string{"/users", "/orders"}}}, wantCode: "route_slug_conflict"},
		{name: "unknown path", choices: []RouteGroupChoice{{Slug: "users", Paths: []string{"/missing"}}}, wantCode: "unknown_path"},
		{name: "invalid slug", choices: []RouteGroupChoice{{Slug: "Not Valid", Paths: []string{"/users"}}}, wantCode: "invalid_route_slug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GroupRoutes(parsed, tt.choices)
			var groupErr *ParseError
			require.ErrorAs(t, err, &groupErr)
			require.Equal(t, tt.wantCode, groupErr.Problems[0].Code)
		})
	}
}

func TestGroupRoutesWithoutServerReturnsPreviewWarning(t *testing.T) {
	parsed := ParsedDocument{Paths: map[string]models.OpenAPIPathItem{"/users": pathWithMethods("GET")}}
	bundle, err := GroupRoutes(parsed, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"users"}, bundle.RouteSlugs())
	require.Contains(t, bundle.Warnings, Problem{Path: "$.servers", Code: "missing_server", Message: "document has no server; an upstream URL must be supplied during import"})
}

func pathWithMethods(methods ...string) models.OpenAPIPathItem {
	operations := make(map[string]models.OpenAPIOperation, len(methods))
	for _, method := range methods {
		operations[method] = models.OpenAPIOperation{OperationID: "operation" + method}
	}
	return models.OpenAPIPathItem{Operations: operations}
}

func mapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
